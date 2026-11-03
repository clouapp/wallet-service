package ramps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
	svc "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"time"
)

var ErrFundingForbidden = errors.New("ramp funding forbidden")

type FundInput struct {
	OrderID          string
	WalletID         uuid.UUID
	Passphrase, Code string
	ActionRevision   int
}

// Fund uses only an order's stored destination/action. The caller supplies
// wallet credentials, never transaction bytes, destination or amount.
func (s *Service) Fund(ctx context.Context, account *models.Account, actor *models.User, customer *models.RampCustomer, in FundInput) (*svc.OrderView, error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if actor == nil || !actor.TotpEnabled {
		return nil, ErrFundingForbidden
	}
	if err := s.d.Features.Gate(ctx, account.ID, features.FlagWithdrawalsEnabled, "withdrawals_paused"); err != nil {
		return nil, err
	}
	row, err := er.NewTransactionRepository().FindByCustomerAndExternalID(customer.EngineCustomerID, in.OrderID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.Type != "offramp" {
		return nil, ErrCustomerNotFound
	}
	wallet, err := s.d.Wallets.FindByID(ctx, in.WalletID)
	if err != nil {
		return nil, err
	}
	if wallet == nil || wallet.AccountID == nil || *wallet.AccountID != account.ID || wallet.MPCCurve != "ed25519" || wallet.Status != "active" {
		return nil, ErrFundingForbidden
	}
	// The account's native chain catalog enforces the same test/prod boundary.
	chain, err := s.d.Chains.FindByID(ctx, wallet.Chain)
	if err != nil {
		return nil, err
	}
	if chain == nil || chain.IsTestnet != (account.Environment == "test") {
		return nil, ErrFundingForbidden
	}
	addresses, err := s.d.Addresses.FindByWalletID(ctx, wallet.ID)
	if err != nil {
		return nil, err
	}
	var signer *models.Address
	for i := range addresses {
		if row.FromAddress != nil && addresses[i].Address == *row.FromAddress {
			signer = &addresses[i]
			break
		}
	}
	if signer == nil {
		return nil, ErrFundingForbidden
	}
	runtime, err := s.Runtime(account, actor)
	if err != nil {
		return nil, err
	}
	auth, err := er.NewOfframpFundingAuthorizationRepository().FindByTransaction(row.ID)
	if err != nil {
		return nil, err
	}
	if auth != nil {
		if auth.State != "prepared" || auth.ActionRevision != in.ActionRevision || !auth.ExpiresAt.After(time.Now()) {
			return nil, ramp.ErrBindingMismatch
		}
		if err := s.d.Verifier.Verify(actor, in.Code, ""); err != nil {
			return nil, err
		}
		encoded, err := s.d.Decrypt(auth.ActionPayloadCiphertext)
		if err != nil {
			return nil, err
		}
		var payload ramp.FundingActionPayload
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return nil, err
		}
		signed, err := sweep.SignStoredRampAction(ctx, s.d.Sweep, wallet, *signer, payload.UnsignedTransaction, auth.UnsignedActionFingerprint, in.Passphrase)
		if err != nil {
			return nil, err
		}
		contact, holder, _, err := s.d.Customers.ContactAndHolder(ctx, customer)
		if err != nil {
			return nil, err
		}
		return runtime.Offramp.Finalize(ctx, contact, holder, dto.FinalizeOfframpDTO{OrderID: in.OrderID, IdempotencyKey: fmt.Sprintf("macro:%s:%d", in.OrderID, auth.ActionRevision), SignedTransaction: signed, ActionRevision: auth.ActionRevision})
	}
	// Deposit funding follows Macro's existing idempotent withdrawal pipeline.
	if row.Status != em.TxStatusPending && row.Status != em.TxStatusAwaitingFunds {
		return runtime.Offramp.Show(ctx, customer.EngineCustomerID, in.OrderID)
	}
	if row.DepositInstructions == nil {
		return nil, ErrFundingForbidden
	}
	var instructions map[string]any
	if err := json.Unmarshal([]byte(*row.DepositInstructions), &instructions); err != nil {
		return nil, err
	}
	to, _ := instructions["to_address"].(string)
	if to == "" {
		to, _ = instructions["address"].(string)
	}
	if to == "" {
		return nil, fmt.Errorf("order has no native funding destination")
	}
	amount := row.AmountToken
	if quoted, ok := instructions["amount"].(string); ok && quoted != "" {
		amount = quoted
	}
	_, err = s.d.Withdraw.Submit(ctx, withdraw.SubmitInput{Wallet: wallet, CallerUserID: actor.ID, CallerAccountID: account.ID, TotpCode: in.Code, Passphrase: in.Passphrase, Asset: row.TargetToken, Amount: amount, DestinationAddress: to, IdempotencyKey: row.ExternalID})
	if err != nil {
		return nil, err
	}
	return runtime.Offramp.Show(ctx, customer.EngineCustomerID, in.OrderID)
}
