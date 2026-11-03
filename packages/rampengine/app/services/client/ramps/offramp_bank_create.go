package ramps

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	bankdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// BankBeneficiaryReader answers one of the customer's bank beneficiaries as
// a payout links to it, or an error carrying ErrBankBeneficiaryNotFound when
// it is not the customer's. *bank.Service satisfies it.
type BankBeneficiaryReader interface {
	GetForPayout(customerID uint, externalID string) (*bankdto.PayoutBeneficiaryDTO, error)
}

// preparedCreate is what a prepared-funding create's gates resolved: the quote,
// its snapshot, the fee of the quote's provider and, on a bank rail, the
// beneficiary as it stands now. build and gates are what differs between the
// rails that are funded this way: the row to reserve, and what a business owes
// once the router resolved.
type preparedCreate struct {
	rail        ramp.Rail
	estimate    *models.RampEstimate
	snapshot    *estimateSnapshot
	beneficiary *bankdto.PayoutBeneficiaryDTO
	fee         string

	build func(customerID, userID uint, provider ramp.ProviderCode, c *preparedCreate) *models.Transaction
	gates func(ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO) error
}

// reservedPrepared is what the create lock resolved: an order already placed
// against the quote, or the reserved row and the provider that prepares it.
type reservedPrepared struct {
	existing *models.Transaction
	tx       *models.Transaction
	preparer ramp.OfframpPrepareAdapter
	ref      ramp.CustomerRef
}

// fundingPayload is the plaintext of an authorization's action payload
// ciphertext.
type fundingPayload struct {
	ContentType         string `json:"content_type"`
	UnsignedTransaction string `json:"unsigned_transaction"`
}

// prepareFailedReason is the failure a reservation records when its funding
// could not be prepared.
const prepareFailedReason = "funding preparation failed"

// createBank places the customer's payout to a registered US bank account,
// for user, and answers it with the funding action the wallet signs (spec
// §5.7, plan Task 6).
//
// Same shape as the Pix create: the lock covers "check dedup, reserve the
// row", and the provider call runs after it. What differs is the call: the
// provider only prepares the funding, which moves no money, so any failure
// frees the reservation and leaves the quote redeemable (plan D6).
func (s *OfframpService) createBank(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
) (*OrderView, bool, error) {
	// A person's step-up comes first; a business's after its gates
	// (bankBusinessGates), as on Pix.
	if stepUpFirst(customer) {
		if err := enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType); err != nil {
			return nil, false, err
		}
	}
	c, replay, err := s.bankCreateGates(ctx, user, customer, in)
	if err != nil {
		return nil, false, err
	}
	if replay != nil {
		return replay, false, nil
	}

	if early, err := s.dedupByEstimate(customer.ID, c.estimate, models.TransactionTypeOfframp); err != nil {
		return nil, false, err
	} else if early != nil && !stale(early) {
		return s.toOrderView(early), false, nil
	}

	reserved, err := s.reservePrepared(ctx, user, customer, in, c)
	if err != nil {
		return nil, false, err
	}
	if reserved.existing != nil {
		return s.toOrderView(reserved.existing), false, nil
	}
	if err := s.prepareFunding(ctx, reserved, c); err != nil {
		return nil, false, err
	}
	s.publish(reserved.tx)
	return s.toOrderView(reserved.tx), true, nil
}

// bankCreateGates runs a bank create's local gates in order: the rail, the
// quote, its binding (the beneficiary's current revision included, plan D7)
// and the rail's limits, against the amount that leaves the wallet. A quote
// an order already consumed answers that order instead (replayBankCreate).
func (s *OfframpService) bankCreateGates(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
) (*preparedCreate, *OrderView, error) {
	rail := ramp.Rail(in.Rail)
	// A service built without the ports cannot place a bank payout; saying
	// so beats a nil dereference further down.
	if s.beneficiaries == nil || s.funding == nil {
		return nil, nil, fmt.Errorf("ramps: bank payout create not wired: %w", ramp.ErrOfframpCreateUnavailable)
	}
	if err := requireRail(ctx, s.features, rail, user.ID); err != nil {
		return nil, nil, err
	}
	policy, err := railPolicy(s.config, rail)
	if err != nil {
		return nil, nil, err
	}
	if in.EstimateID == "" {
		return nil, nil, ramp.ErrEstimateRequired
	}

	estimate, snapshot, err := s.loadEstimate(customer.ID, in.EstimateID, rail.String())
	if errors.Is(err, ramp.ErrEstimateConsumed) {
		view, err := s.replayPreparedCreate(customer.ID, in, bindBankCreate)
		return nil, view, err
	}
	if err != nil {
		return nil, nil, err
	}
	if err := bindBankCreate(in, snapshot); err != nil {
		return nil, nil, err
	}
	beneficiary, err := s.beneficiaries.GetForPayout(customer.ID, in.BankBeneficiaryID)
	if err != nil {
		return nil, nil, err
	}
	// The provider account the quote priced was registered for that
	// revision: an edit since then is not paid at the quoted terms.
	if beneficiary.Revision != snapshot.BeneficiaryRevision {
		return nil, nil, ramp.ErrBeneficiaryChanged
	}
	if err := checkLimitsUSD(policy, snapshot.SourceAmount); err != nil {
		return nil, nil, err
	}
	fee, err := policy.FeeFor(estimate.Provider)
	if err != nil {
		return nil, nil, err
	}
	return &preparedCreate{
		rail: rail, estimate: estimate, snapshot: snapshot, beneficiary: beneficiary, fee: fee,
		build: newBankTransaction, gates: s.bankBusinessGates,
	}, nil, nil
}

// replayPreparedCreate answers a create naming a quote an order already
// consumed with that order, and its funding action while it is valid. A
// double tap lands on the same order and action, never a second prepare
// (plan Review Focus 3). A Pix quote of a provider that takes a deposit
// answers estimate_consumed instead: that create consumes the quote only
// once the provider holds the order. bind is the rail's own comparison of the
// request with the quote.
func (s *OfframpService) replayPreparedCreate(
	customerID uint, in dto.CreateOfframpDTO, bind func(dto.CreateOfframpDTO, *estimateSnapshot) error,
) (*OrderView, error) {
	row, err := s.estimateRepo.FindByCustomerAndExternalID(customerID, in.EstimateID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload consumed estimate: %w", err)
	}
	if row == nil {
		return nil, ramp.ErrEstimateConsumed
	}
	snapshot, ok := decodeEstimateSnapshot(row.RawResponse)
	if !ok {
		return nil, errors.New("ramps: decode consumed estimate snapshot")
	}
	if err := bind(in, snapshot); err != nil {
		return nil, err
	}
	tx, err := s.dedupByEstimate(customerID, row, models.TransactionTypeOfframp)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, ramp.ErrEstimateConsumed
	}
	return s.toOrderView(tx), nil
}

// reservePrepared holds the quote under the create lock: an order already
// placed against it is answered, a stale reservation resumed, and otherwise
// a new row reserved. The quote's provider is resolved sticky, and must be
// able to prepare a funding action.
func (s *OfframpService) reservePrepared(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO, c *preparedCreate,
) (*reservedPrepared, error) {
	r := &reservedPrepared{}
	err := s.locker.WithLock(ctx, createLockKey(models.TransactionTypeOfframp, customer.ID, in.EstimateID),
		func(ctx context.Context) error {
			found, err := s.dedupByEstimate(customer.ID, c.estimate, models.TransactionTypeOfframp)
			if err != nil {
				return err
			}
			if found != nil && !stale(found) {
				r.existing = found
				return nil
			}
			adapter, ref, err := s.router.ResolveRedemption(ctx, customer.ID, c.rail, c.estimate.Provider)
			if err != nil {
				return err
			}
			if err := c.gates(ctx, user, customer, in); err != nil {
				return err
			}
			preparer, ok := adapter.(ramp.OfframpPrepareAdapter)
			if !ok {
				return fmt.Errorf("ramps: %s prepares no offramp funding: %w", adapter.Code(), ramp.ErrOfframpCreateUnavailable)
			}
			r.preparer, r.ref = preparer, ref
			if found != nil {
				r.tx = found
				return s.resume(found)
			}
			r.tx = c.build(customer.ID, user.ID, adapter.Code(), c)
			if err := s.reserve(r.tx); err != nil {
				return fmt.Errorf("ramps: persist bank offramp: %w", err)
			}
			return nil
		})
	return r, err
}

// bankBusinessGates is what a business's bank payout owes once the router
// resolved, before anything is reserved: its money rules (active, its own
// wallet), then the step-up. A person owes neither here.
func (s *OfframpService) bankBusinessGates(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.CreateOfframpDTO,
) error {
	if stepUpFirst(customer) {
		return nil
	}
	if err := s.rules.bankOfframp(customer, in.Chain, in.FromAddress); err != nil {
		return err
	}
	return enforceMfaStepUp(ctx, s.mfaStepUp, user, in.Code, in.CodeType)
}

// newBankTransaction builds the row a bank create reserves, from the quote it
// is bound to. It stays pending until its funding authorization is recorded:
// the repository makes it await the funds in that same write, and only from
// pending, so a reservation expired meanwhile is never resurrected.
func newBankTransaction(customerID, userID uint, provider ramp.ProviderCode, c *preparedCreate) *models.Transaction {
	tx := newTransaction(customerID, userID, provider, models.TransactionTypeOfframp, c.rail)
	// A bank payout has no BRL leg: AmountFiat is the USD that lands.
	tx.AmountToken = c.snapshot.SourceAmount
	tx.TargetToken = c.snapshot.SourceCurrency
	from, chain := c.snapshot.FromAddress, c.snapshot.Chain
	tx.FromAddress, tx.Chain = &from, &chain
	rail := c.rail.String()
	tx.AmountFiat, tx.FiatCurrency = c.snapshot.EstimatedReceive, models.FiatCurrencyUSD
	beneficiaryID := c.beneficiary.ID
	tx.BankBeneficiaryID = &beneficiaryID
	tx.BankRail = &rail
	accountType, accountLast4, routingLast4 := c.beneficiary.AccountType, c.beneficiary.AccountLast4, c.beneficiary.RoutingLast4
	tx.BankAccountType, tx.BankAccountLast4, tx.BankRoutingLast4 = &accountType, &accountLast4, &routingLast4
	fee := c.fee
	tx.FeePercent = &fee
	estimateID := c.estimate.ID
	tx.EstimateID = &estimateID
	return tx
}

// prepareFunding asks the provider to prepare the reserved order's funding
// and records the prepared action, consuming the quote in the same database
// transaction. Any failure frees the reservation, so the quote stays
// redeemable (plan D6). The provider order (po_) is E7's: GatewayOrderID
// stays empty.
func (s *OfframpService) prepareFunding(ctx context.Context, r *reservedPrepared, c *preparedCreate) error {
	prepared, err := r.preparer.PrepareOfframp(ctx, r.ref, prepareRequest(c.estimate, c.snapshot))
	if err == nil {
		err = checkPrepared(prepared)
	}
	if err != nil {
		s.failReservation(r.tx, prepareFailedReason)
		return fmt.Errorf("ramps: prepare offramp funding: %w", err)
	}
	auth, err := sealFundingAuthorization(r.tx, prepared)
	if err != nil {
		s.failReservation(r.tx, prepareFailedReason)
		return err
	}
	// The order can be funded while its quote lives, not only for the
	// reservation's window: the reconciler expires the row at this deadline.
	// The repository writes it with the authorization and the quote's
	// consumption, so none of the three lands alone.
	fundableUntil := deref(r.tx.ExpiresAt)
	if c.estimate.ExpiresAt != nil {
		fundableUntil = *c.estimate.ExpiresAt
	}
	if err := s.funding.CreatePreparedConsumingEstimate(auth, c.estimate.ID, fundableUntil); err != nil {
		s.failReservation(r.tx, prepareFailedReason)
		return fmt.Errorf("ramps: record funding authorization: %w", err)
	}
	r.tx.Status, r.tx.ExpiresAt = models.TxStatusAwaitingFunds, &fundableUntil
	return nil
}

// prepareRequest is the provider's prepare request, rebuilt from the quote and
// its snapshot: the create and every refresh of the action ask the same thing.
func prepareRequest(estimate *models.RampEstimate, snapshot *estimateSnapshot) ramp.OfframpPrepareRequest {
	return ramp.OfframpPrepareRequest{
		ProviderQuoteID: deref(estimate.ProviderQuoteID),
		OwnerAddress:    snapshot.FromAddress,
		Token:           snapshot.SourceCurrency,
		Chain:           snapshot.Chain,
		SourceAmount:    snapshot.SourceAmount,
		QuoteExpiresAt:  deref(estimate.ExpiresAt),
	}
}

// checkPrepared refuses a prepare answer this side cannot use, as the
// provider's failure: an action the contract does not carry, one already
// expired, or no fingerprint of the message to be signed.
func checkPrepared(p *ramp.PreparedOfframpFunding) error {
	if p == nil {
		return fmt.Errorf("%w: empty prepare answer", ramp.ErrProviderFailed)
	}
	if err := p.Action.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ramp.ErrProviderFailed, err)
	}
	if !p.Action.ExpiresAt.After(time.Now()) {
		return fmt.Errorf("%w: funding action already expired", ramp.ErrProviderFailed)
	}
	if raw, err := hex.DecodeString(p.MessageFingerprint); err != nil || len(raw) != 32 {
		return fmt.Errorf("%w: funding action fingerprint is not a SHA-256 hex", ramp.ErrProviderFailed)
	}
	return nil
}

// sealFundingAuthorization builds the prepared authorization of tx, its
// provider state and action payload encrypted with facades.Crypt(): the
// payload as fundingPayload JSON, the state as the base64 of its bytes,
// which are opaque here.
func sealFundingAuthorization(tx *models.Transaction, p *ramp.PreparedOfframpFunding) (*models.OfframpFundingAuthorization, error) {
	sealedState, sealedPayload, err := sealPrepared(p)
	if err != nil {
		return nil, err
	}
	return &models.OfframpFundingAuthorization{
		TransactionID:             tx.ID,
		Provider:                  tx.Provider,
		ActionType:                p.Action.Type,
		SubmissionMode:            p.Action.SubmissionMode,
		ProviderStateCiphertext:   sealedState,
		ActionPayloadCiphertext:   sealedPayload,
		UnsignedActionFingerprint: p.MessageFingerprint,
		ExpiresAt:                 p.Action.ExpiresAt,
		ActionRevision:            1,
	}, nil
}

// sealPrepared encrypts what a prepare answered and must not rest in the
// clear: the provider state and the action payload.
func sealPrepared(p *ramp.PreparedOfframpFunding) (state, payload string, err error) {
	plain, err := json.Marshal(fundingPayload{
		ContentType: p.Action.Payload.ContentType, UnsignedTransaction: p.Action.Payload.UnsignedTransaction,
	})
	if err != nil {
		return "", "", fmt.Errorf("ramps: encode funding action: %w", err)
	}
	payload, err = facades.Crypt().EncryptString(string(plain))
	if err != nil {
		return "", "", fmt.Errorf("ramps: encrypt funding action: %w", err)
	}
	state, err = facades.Crypt().EncryptString(base64.StdEncoding.EncodeToString(p.CorrelationState))
	if err != nil {
		return "", "", fmt.Errorf("ramps: encrypt funding state: %w", err)
	}
	return state, payload, nil
}

// openFundingPayload decrypts an authorization's action payload.
func openFundingPayload(ciphertext string) (fundingPayload, error) {
	plaintext, err := facades.Crypt().DecryptString(ciphertext)
	if err != nil {
		return fundingPayload{}, fmt.Errorf("ramps: decrypt funding action: %w", err)
	}
	var payload fundingPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		return fundingPayload{}, fmt.Errorf("ramps: decode funding action: %w", err)
	}
	return payload, nil
}
