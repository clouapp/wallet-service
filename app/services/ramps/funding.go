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
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"slices"
	"time"
)

var ErrFundingForbidden = errors.New("ramp funding forbidden")

// FundingNotSubmittedError is a refusal before this request could submit funds.
// Unknown provider/broadcast outcomes deliberately never use this proof.
type FundingNotSubmittedError struct{ Cause error }

func (e *FundingNotSubmittedError) Error() string { return "ramp funding was not submitted" }
func (e *FundingNotSubmittedError) Unwrap() error { return e.Cause }

type FundInput struct {
	APIToken         *models.AccessToken
	OrderID          string
	WalletID         uuid.UUID
	Passphrase, Code string
	ActionRevision   int
}

// Fund uses only an order's stored destination/action. The caller supplies
// wallet credentials, never transaction bytes, destination or amount.
func (s *Service) Fund(ctx context.Context, account *models.Account, actor *models.User, customer *models.RampCustomer, in FundInput) (result *svc.OrderView, err error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if account == nil || (actor == nil && in.APIToken == nil) || (actor != nil && !actor.TotpEnabled) || (in.APIToken != nil && in.APIToken.AccountID != account.ID) {
		return nil, &FundingNotSubmittedError{Cause: ErrFundingForbidden}
	}
	orderID, parseErr := uuid.Parse(in.OrderID)
	if parseErr != nil {
		return nil, &FundingNotSubmittedError{Cause: ErrCustomerNotFound}
	}
	in.OrderID = orderID.String()
	err = locking.NewAdvisoryLocker().WithLock(ctx, fundingLockKey(orderID), func(locked context.Context) error {
		result, err = s.fundLocked(locked, account, actor, customer, in)
		return err
	})
	return result, err
}

func fundingLockKey(orderID uuid.UUID) string { return "macro-ramp-funding:" + orderID.String() }

func (s *Service) fundLocked(ctx context.Context, account *models.Account, actor *models.User, customer *models.RampCustomer, in FundInput) (result *svc.OrderView, err error) {
	possibleSubmission := false
	defer func() {
		if err != nil && !possibleSubmission {
			err = &FundingNotSubmittedError{Cause: err}
		}
	}()
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if customer == nil || customer.AccountID != account.ID {
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
		if auth.State != "prepared" {
			possibleSubmission = true
		}
		if auth.State != "prepared" || auth.ActionRevision != in.ActionRevision || !auth.ExpiresAt.After(time.Now()) {
			return nil, ramp.ErrBindingMismatch
		}
		if actor != nil {
			if err := s.d.Verifier.Verify(actor, in.Code, ""); err != nil {
				return nil, err
			}
		}
		if err := s.d.Withdraw.AuthorizeRampFunding(ctx, in.APIToken, row.ExternalID, row.TargetToken, row.AmountToken); err != nil {
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
		possibleSubmission = true
		result, err = runtime.Offramp.Finalize(ctx, contact, holder, dto.FinalizeOfframpDTO{OrderID: in.OrderID, IdempotencyKey: fmt.Sprintf("macro:%s:%d", in.OrderID, auth.ActionRevision), SignedTransaction: signed, ActionRevision: auth.ActionRevision})
		var refused *ramp.FinalizeRefusedError
		if errors.As(err, &refused) {
			possibleSubmission = false
		}
		return result, err
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
	caller := uuid.Nil
	if actor != nil {
		caller = actor.ID
	}
	possibleSubmission = true
	_, err = s.d.Withdraw.Submit(ctx, withdraw.SubmitInput{APIToken: in.APIToken, Wallet: wallet, CallerUserID: caller, CallerAccountID: account.ID, TotpCode: in.Code, Passphrase: in.Passphrase, Asset: row.TargetToken, Amount: amount, DestinationAddress: to, IdempotencyKey: row.ExternalID})
	if err != nil {
		var refusal *withdraw.CreateRefusal
		var rowError *withdraw.CreateRowError
		if errors.As(err, &refusal) || (errors.As(err, &rowError) && slices.Contains([]string{"create_wallet_withdrawal", "find_idempotent_withdrawal", "create_broadcasting_withdrawal", "retry_broadcasting_withdrawal", "withdrawal_key_lock"}, rowError.Endpoint)) {
			possibleSubmission = false
		}
		return nil, err
	}
	return runtime.Offramp.Show(ctx, customer.EngineCustomerID, in.OrderID)
}

// FundingWasNotSubmitted serializes with funding before checking a terminal
// refusal or the absence of native deposit-funding intent for an owned wallet.
func (s *Service) FundingWasNotSubmitted(ctx context.Context, customer *models.RampCustomer, orderID string, walletID uuid.UUID) (notSubmitted bool, err error) {
	id, err := uuid.Parse(orderID)
	if err != nil || customer == nil {
		return false, ErrCustomerNotFound
	}
	err = locking.NewAdvisoryLocker().WithLock(ctx, fundingLockKey(id), func(locked context.Context) error {
		notSubmitted, err = s.fundingEvidenceLocked(locked, customer, id.String(), walletID)
		return err
	})
	return notSubmitted, err
}

func (s *Service) fundingEvidenceLocked(ctx context.Context, customer *models.RampCustomer, orderID string, walletID uuid.UUID) (bool, error) {
	row, err := er.NewTransactionRepository().FindByCustomerAndExternalID(customer.EngineCustomerID, orderID)
	if err != nil {
		return false, err
	}
	if row == nil || row.Type != "offramp" {
		return false, ErrCustomerNotFound
	}
	if row.Status != em.TxStatusFailed && row.Status != em.TxStatusExpired {
		return false, nil
	}
	auth, err := er.NewOfframpFundingAuthorizationRepository().FindByTransaction(row.ID)
	if err != nil {
		return false, err
	}
	if auth != nil {
		return auth.State == em.FundingAuthFailed, nil
	}
	if walletID == uuid.Nil || row.DepositInstructions == nil || row.FromAddress == nil {
		return false, nil
	}
	wallet, err := s.d.Wallets.FindByID(ctx, walletID)
	if err != nil {
		return false, err
	}
	if wallet == nil || wallet.AccountID == nil || *wallet.AccountID != customer.AccountID {
		return false, ErrFundingForbidden
	}
	addresses, err := s.d.Addresses.FindByWalletID(ctx, wallet.ID)
	if err != nil {
		return false, err
	}
	for _, address := range addresses {
		if address.Address == *row.FromAddress {
			// Submit persists its deterministic withdrawal ID before any broadcast.
			// A terminal order cannot fund after this shared critical section.
			return s.d.State.FundingIntentAbsent(ctx, uuid.MustParse(orderID))
		}
	}
	return false, ErrFundingForbidden
}
