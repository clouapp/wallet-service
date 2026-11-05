package ramps

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

const (
	// finalizeKeyMaxLength bounds the client's Idempotency-Key.
	finalizeKeyMaxLength = 128
	// finalizeSendTimeout bounds what a finalize sends: the delegation to the
	// chain and its confirmation (up to 15 s), then the one POST.
	finalizeSendTimeout = 45 * time.Second
	// finalizeLease is how long the sender owns a finalizing row before the
	// recovery may take it: the recovery's own lease, well above the send's
	// timeout (the writes after the send are not fenced on it).
	finalizeLease = 2 * time.Minute
	// finalizeFirstRetry is when the recovery first looks at an unknown
	// outcome: 2^0 minutes of its backoff (plan D8).
	finalizeFirstRetry = time.Minute
	// signedPayloadRetention is how long the signed bytes stay stored
	// (encrypted) for the recovery to send them again: past its 24 h window
	// and every attempt.
	signedPayloadRetention = 720 * time.Hour
	// providerFinalizeKeyPrefix makes the provider's Idempotency-Key from the
	// authorization, never from the client (plan D4).
	providerFinalizeKeyPrefix = "bank-finalize:"
	// failureCodeMaxLength is the failure_code column.
	failureCodeMaxLength = 64

	// The codes an unknown outcome is marked with, for the recovery's log: a
	// used quote is looked up by quote, anything else sent again.
	unknownQuoteAlreadyUsed = "quote_already_used"
	unknownAmbiguous        = "finalize_ambiguous"
	// refusedWithoutCode is a definitive refusal the provider named no code for.
	refusedWithoutCode = "provider_refused"
)

// ErrIdempotencyKeyRequired is a finalize without a usable Idempotency-Key;
// ErrIdempotencyKeyReused is a key replayed with another body. Both alias the
// adapter's sentinels, so errors.Is holds through the service's wrapping.
var (
	ErrIdempotencyKeyRequired = ramp.ErrInvalidIdempotencyKey
	ErrIdempotencyKeyReused   = ramp.ErrIdempotencyKeyReused
)

// finalizePorts are the optional ports of the order's provider a finalize
// needs, and the customer they act for.
type finalizePorts struct {
	sender    ramp.OfframpFinalizeAdapter
	validator ramp.SignedOfframpActionValidator
	legs      ramp.PayoutLegNamer
	ref       ramp.CustomerRef
}

// finalizeSend is what the lock decided to send: the intent is committed.
type finalizeSend struct {
	tx     *models.Transaction
	auth   *models.OfframpFundingAuthorization
	sender ramp.OfframpFinalizeAdapter
	ref    ramp.CustomerRef
	req    ramp.FinalizeRequest
}

// finalizeKeys are the hashes a finalize is recognized by (plan D4).
type finalizeKeys struct{ client, request string }

// Finalize sends the wallet's signed funding action of the customer's bank
// payout to the provider, which creates the payout (spec §5.8, plan Task 6).
//
// Under the order's funding lock it decides (replay, state, rail, revision,
// quote, action, ports, signature) and commits the intent: the authorization
// finalizing, with the signed bytes encrypted and the provider's key. The lock
// is released before the POST, which is sent once, outside it; its outcome is
// written by one repository transition (plan D7). Anything the provider
// answered that is not a recognized outcome is unknown, and the recovery
// closes it: a wrong "failed" is how a payout is paid twice.
func (s *OfframpService) Finalize(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.FinalizeOfframpDTO,
) (*OrderView, error) {
	if in.IdempotencyKey == "" || utf8.RuneCountInString(in.IdempotencyKey) > finalizeKeyMaxLength {
		return nil, ErrIdempotencyKeyRequired
	}
	tx, err := s.txRepo.FindByCustomerAndExternalID(customer.ID, in.OrderID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load offramp: %w", err)
	}
	if tx == nil || tx.Type != models.TransactionTypeOfframp {
		return nil, ErrOrderNotFound
	}
	if !tx.FundedByAuthorization() {
		return nil, fmt.Errorf("ramps: finalize offramp %s on rail %q: %w", tx.ExternalID, deref(tx.Rail), ErrInvalidOrderState)
	}
	if s.funding == nil {
		return nil, fmt.Errorf("ramps: finalize not wired: %w", ramp.ErrOfframpCreateUnavailable)
	}
	var (
		send   *finalizeSend
		answer *OrderView
	)
	err = s.locker.WithLock(ctx, fundingLockKey(tx.ID), func(ctx context.Context) error {
		var err error
		send, answer, err = s.finalizeLocked(ctx, user, customer, in)
		return err
	})
	if err != nil || answer != nil {
		return answer, err
	}
	return s.sendFinalize(ctx, send)
}

// finalizeLocked is Finalize's decision and intent, under the funding lock,
// on the order and authorization read again. It answers either the order (a
// replay) or the send the intent committed.
func (s *OfframpService) finalizeLocked(
	ctx context.Context, user *models.User, customer *models.Customer, in dto.FinalizeOfframpDTO,
) (*finalizeSend, *OrderView, error) {
	tx, err := s.txRepo.FindByCustomerAndExternalID(customer.ID, in.OrderID)
	if err != nil {
		return nil, nil, fmt.Errorf("ramps: reload offramp: %w", err)
	}
	if tx == nil {
		return nil, nil, ErrOrderNotFound
	}
	auth, err := s.funding.FindByTransaction(tx.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("ramps: load funding authorization: %w", err)
	}
	if auth == nil {
		return nil, nil, fmt.Errorf("ramps: offramp %s has no funding authorization: %w", tx.ExternalID, ErrInvalidOrderState)
	}
	keys := finalizeKeysOf(in, auth)
	if replay, err := s.repeatedFinalize(tx, auth, keys); replay != nil || err != nil {
		return nil, replay, err
	}
	if err := requireRail(ctx, s.features, ramp.Rail(deref(tx.Rail)), user.ID); err != nil {
		return nil, nil, err
	}
	estimate, snapshot, err := s.finalizableAction(tx, auth, in.ActionRevision)
	if err != nil {
		return nil, nil, err
	}
	ports, err := s.finalizePortsOf(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if err := validateSignedAction(ctx, ports, auth, in.SignedTransaction); err != nil {
		return nil, nil, err
	}
	return s.beginFinalize(ctx, tx, auth, keys, in, ports, estimate, snapshot)
}

// finalizeKeysOf hashes the client's key and the request (plan D4): the
// client's key is never stored in the clear.
func finalizeKeysOf(in dto.FinalizeOfframpDTO, auth *models.OfframpFundingAuthorization) finalizeKeys {
	return finalizeKeys{
		client:  sha256Hex(in.IdempotencyKey + ":" + auth.ExternalID),
		request: sha256Hex(in.SignedTransaction + ":" + strconv.Itoa(in.ActionRevision)),
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// repeatedFinalize answers a finalize this authorization already took (plan
// D6): the same key and body is the order as it stands, the same key with
// another body is ErrIdempotencyKeyReused. Otherwise only a prepared
// authorization of an order awaiting its funds goes on; anything else is
// ErrInvalidOrderState. It answers (nil, nil) when the finalize goes on.
func (s *OfframpService) repeatedFinalize(
	tx *models.Transaction, auth *models.OfframpFundingAuthorization, keys finalizeKeys,
) (*OrderView, error) {
	if auth.FinalizeClientKeyHash != nil && *auth.FinalizeClientKeyHash == keys.client {
		if auth.FinalizeRequestHash != nil && *auth.FinalizeRequestHash == keys.request {
			return s.toOrderView(tx), nil
		}
		return nil, fmt.Errorf("ramps: finalize offramp %s: %w", tx.ExternalID, ErrIdempotencyKeyReused)
	}
	if auth.State != models.FundingAuthPrepared || tx.Status != models.TxStatusAwaitingFunds {
		return nil, fmt.Errorf("ramps: finalize offramp %s in %s/%s: %w", tx.ExternalID, tx.Status, auth.State, ErrInvalidOrderState)
	}
	return nil, nil
}

// finalizableAction holds the signed action to what can still be funded: the
// current revision, a live quote (a lapsed one fails the authorization and
// expires the order, as the refresh does) and an action within its expiry.
// It answers the quote and its snapshot.
func (s *OfframpService) finalizableAction(
	tx *models.Transaction, auth *models.OfframpFundingAuthorization, revision int,
) (*models.RampEstimate, *estimateSnapshot, error) {
	if revision != auth.ActionRevision {
		return nil, nil, ramp.ErrFundingActionExpired
	}
	if tx.EstimateID == nil {
		return nil, nil, fmt.Errorf("ramps: offramp %s has no estimate", tx.ExternalID)
	}
	estimate, err := s.estimateRepo.FindByCustomerAndID(tx.CustomerID, *tx.EstimateID)
	if err != nil {
		return nil, nil, fmt.Errorf("ramps: load estimate of offramp %s: %w", tx.ExternalID, err)
	}
	if estimate == nil {
		return nil, nil, fmt.Errorf("ramps: estimate of offramp %s is missing", tx.ExternalID)
	}
	snapshot, ok := decodeEstimateSnapshot(estimate.RawResponse)
	if !ok {
		return nil, nil, fmt.Errorf("ramps: decode estimate snapshot of offramp %s", tx.ExternalID)
	}
	now := time.Now()
	if !deref(estimate.ExpiresAt).After(now) {
		changed, err := s.funding.FailAndExpire(auth, models.FundingFailureQuoteExpired)
		if err != nil {
			return nil, nil, fmt.Errorf("ramps: expire offramp %s: %w", tx.ExternalID, err)
		}
		if changed {
			tx.Status = models.TxStatusExpired
			s.publish(tx)
		}
		return nil, nil, ramp.ErrFundingActionExpired
	}
	if !auth.ExpiresAt.After(now) {
		return nil, nil, ramp.ErrFundingActionExpired
	}
	return estimate, snapshot, nil
}

// finalizePortsOf resolves the order's own provider, without the eligibility
// gates (the flag already passed), and finds the three ports a finalize
// needs. A provider without them cannot finalize.
func (s *OfframpService) finalizePortsOf(ctx context.Context, tx *models.Transaction) (*finalizePorts, error) {
	adapter, ref, err := s.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return nil, fmt.Errorf("ramps: resolve provider to finalize offramp %s: %w", tx.ExternalID, err)
	}
	sender, canSend := adapter.(ramp.OfframpFinalizeAdapter)
	validator, canValidate := adapter.(ramp.SignedOfframpActionValidator)
	legs, canName := adapter.(ramp.PayoutLegNamer)
	if !canSend || !canValidate || !canName {
		return nil, fmt.Errorf("ramps: %s cannot finalize an offramp: %w", tx.Provider, ramp.ErrOfframpCreateUnavailable)
	}
	return &finalizePorts{sender: sender, validator: validator, legs: legs, ref: ref}, nil
}

// validateSignedAction checks the signed transaction against what was
// prepared. ErrInvalidSignedActionPayload passes as it is (the wallet signs
// again); any other failure is this side's and retryable. Neither touches
// the authorization.
func validateSignedAction(
	ctx context.Context, ports *finalizePorts, auth *models.OfframpFundingAuthorization, signed string,
) error {
	state, err := openFundingState(auth.ProviderStateCiphertext)
	if err != nil {
		return err
	}
	err = ports.validator.ValidateSignedOfframpAction(ctx, ports.ref, ramp.SignedOfframpActionValidationRequest{
		SignedTransaction: signed, MessageFingerprint: auth.UnsignedActionFingerprint, CorrelationState: state,
	})
	if errors.Is(err, ramp.ErrInvalidSignedActionPayload) {
		return fmt.Errorf("ramps: signed funding action: %w", err)
	}
	if err != nil {
		return fmt.Errorf("ramps: validate signed funding action: %v", err)
	}
	return nil
}

// openFundingState decrypts the provider state a prepare stored (the base64
// of its bytes, encrypted).
func openFundingState(ciphertext string) ([]byte, error) {
	encoded, err := facades.Crypt().DecryptString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("ramps: decrypt funding state: %w", err)
	}
	state, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("ramps: decode funding state: %w", err)
	}
	return state, nil
}

// beginFinalize builds the binding, encrypts the signed bytes and commits the
// intent (plan D3, D4). A swap lost to another writer answers as that writer
// left the authorization: a replay of this call, or an invalid state.
func (s *OfframpService) beginFinalize(
	ctx context.Context, tx *models.Transaction, auth *models.OfframpFundingAuthorization, keys finalizeKeys,
	in dto.FinalizeOfframpDTO, ports *finalizePorts, estimate *models.RampEstimate, snapshot *estimateSnapshot,
) (*finalizeSend, *OrderView, error) {
	binding, err := payoutBinding(ctx, ports, estimate, snapshot)
	if err != nil {
		return nil, nil, fmt.Errorf("ramps: bind payout of offramp %s: %w", tx.ExternalID, err)
	}
	intent, err := finalizeIntent(auth, keys, in.SignedTransaction, binding)
	if err != nil {
		return nil, nil, err
	}
	won, err := s.funding.BeginFinalize(auth, auth.ActionRevision, intent)
	if err != nil {
		return nil, nil, fmt.Errorf("ramps: begin finalize of offramp %s: %w", tx.ExternalID, err)
	}
	if !won {
		view, err := s.lostFinalize(tx, keys)
		return nil, view, err
	}
	return &finalizeSend{
		tx: tx, auth: auth, sender: ports.sender, ref: ports.ref,
		req: ramp.FinalizeRequest{OrderBinding: binding, SignedTransaction: in.SignedTransaction, IdempotencyKey: intent.ProviderKey},
	}, nil, nil
}

// lostFinalize answers a finalize whose intent lost the swap, from the
// order and authorization as they stand now: a replay of this very call is
// the order, anything else the refusal a fresh call would get.
func (s *OfframpService) lostFinalize(tx *models.Transaction, keys finalizeKeys) (*OrderView, error) {
	auth, err := s.funding.FindByTransaction(tx.ID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload funding authorization: %w", err)
	}
	if auth == nil {
		return nil, fmt.Errorf("ramps: offramp %s lost its funding authorization: %w", tx.ExternalID, ErrInvalidOrderState)
	}
	current, err := s.txRepo.FindByCustomerAndExternalID(tx.CustomerID, tx.ExternalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload offramp %s: %w", tx.ExternalID, err)
	}
	if current == nil {
		return nil, ErrOrderNotFound
	}
	if replay, err := s.repeatedFinalize(current, auth, keys); replay != nil || err != nil {
		return replay, err
	}
	// Still prepared and awaiting its funds, yet the swap failed: the
	// action's revision moved, so the signed one is stale.
	return nil, ramp.ErrFundingActionExpired
}

// payoutBinding is what the payout must be, in the provider's spelling: the
// quote, the account and customer the quote priced, and the leg the wallet
// funds (plan D3).
func payoutBinding(
	ctx context.Context, ports *finalizePorts, estimate *models.RampEstimate, snapshot *estimateSnapshot,
) (ramp.OrderBinding, error) {
	if snapshot.CustomerExternalID != ports.ref.ExternalID {
		return ramp.OrderBinding{}, errors.New("the quote names another provider customer")
	}
	network, token, err := ports.legs.PayoutLeg(ctx, snapshot.Chain, snapshot.SourceCurrency)
	if err != nil {
		return ramp.OrderBinding{}, fmt.Errorf("name the payout leg: %w", err)
	}
	b := ramp.OrderBinding{
		ProviderCustomerID: snapshot.CustomerExternalID, BankAccountID: snapshot.BankAccountID,
		QuoteID: deref(estimate.ProviderQuoteID), Network: network, Token: token,
		SenderAmount: snapshot.SourceAmount, SenderWallet: snapshot.FromAddress,
	}
	for _, field := range []string{b.ProviderCustomerID, b.BankAccountID, b.QuoteID, b.Network, b.Token, b.SenderAmount, b.SenderWallet} {
		if field == "" {
			return ramp.OrderBinding{}, errors.New("the binding is incomplete")
		}
	}
	return b, nil
}

// finalizeIntent is what BeginFinalize writes: the hashed keys, the
// provider's key, the signed bytes encrypted, the binding, and the lease.
func finalizeIntent(
	auth *models.OfframpFundingAuthorization, keys finalizeKeys, signed string, binding ramp.OrderBinding,
) (repositories.FinalizeIntent, error) {
	if auth.ExternalID == "" {
		return repositories.FinalizeIntent{}, errors.New("ramps: funding authorization has no external id")
	}
	sealed, err := facades.Crypt().EncryptString(signed)
	if err != nil {
		return repositories.FinalizeIntent{}, fmt.Errorf("ramps: encrypt signed funding action: %w", err)
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return repositories.FinalizeIntent{}, fmt.Errorf("ramps: encode payout binding: %w", err)
	}
	now := time.Now()
	return repositories.FinalizeIntent{
		ClientKeyHash: keys.client, RequestHash: keys.request,
		ProviderKey:             providerFinalizeKeyPrefix + auth.ExternalID,
		SignedPayloadCiphertext: sealed, PayoutBinding: string(encoded),
		StartedAt: now, LeaseUntil: now.Add(finalizeLease), RetainUntil: now.Add(signedPayloadRetention),
	}, nil
}

// sendFinalize sends the payout once, outside the lock and bounded, and
// writes its outcome. A client that goes away does not cut the POST short:
// the money may already be moving, and the outcome is written either way.
func (s *OfframpService) sendFinalize(ctx context.Context, send *finalizeSend) (*OrderView, error) {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeSendTimeout)
	order, sendErr := send.sender.FinalizeOfframp(sendCtx, send.ref, send.req)
	cancel()

	changed, err := s.recordFinalize(ctx, send, order, sendErr)
	if err != nil {
		facades.Log().WithContext(ctx).With(map[string]any{"order": send.tx.ExternalID, "authorization": send.auth.ExternalID}).
			Errorf("ramp.attention: finalize outcome not written, left finalizing for the recovery: %v", err)
		return nil, fmt.Errorf("ramps: record finalize outcome of offramp %s: %w", send.tx.ExternalID, err)
	}
	current, err := s.txRepo.FindByCustomerAndExternalID(send.tx.CustomerID, send.tx.ExternalID)
	if err != nil {
		return nil, fmt.Errorf("ramps: reload offramp %s: %w", send.tx.ExternalID, err)
	}
	if current == nil {
		return nil, ErrOrderNotFound
	}
	if changed {
		s.publish(current)
	}
	return s.toOrderView(current), nil
}

// recordFinalize writes the send's outcome by the table (plan D7), each by
// one repository transition. Only a payout, a lapsed quote, a used quote and
// a definitive refusal are read; anything else is unknown. It reports
// whether the write landed (false: the recovery closed the row first).
func (s *OfframpService) recordFinalize(
	ctx context.Context, send *finalizeSend, order *ramp.RampOrder, sendErr error,
) (bool, error) {
	log := facades.Log().WithContext(ctx).With(map[string]any{"order": send.tx.ExternalID, "authorization": send.auth.ExternalID})
	var refused *ramp.FinalizeRefusedError
	switch {
	case sendErr == nil && order != nil && order.ProviderOrderID != "":
		log.Infof("ramps: finalize created payout %s", order.ProviderOrderID)
		return s.funding.CompleteFinalized(send.auth, order.ProviderOrderID, order.TxHash)
	case errors.Is(sendErr, ramp.ErrEstimateExpired):
		log.Infof("ramps: finalize refused, the quote lapsed")
		return s.funding.CompleteFailed(send.auth, models.FundingFailureQuoteExpired, models.TxStatusExpired)
	case errors.Is(sendErr, ramp.ErrQuoteAlreadyUsed):
		log.Warningf("ramps: finalize outcome unknown, the quote is already used")
		return s.funding.MarkUnknown(send.auth, unknownQuoteAlreadyUsed, time.Now().Add(finalizeFirstRetry))
	case errors.As(sendErr, &refused):
		log.Infof("ramps: finalize refused by the provider: %s", refused.Code)
		return s.funding.CompleteFailed(send.auth, refusalCode(refused), models.TxStatusFailed)
	default:
		log.Warningf("ramps: finalize outcome unknown: %v", sendErr)
		return s.funding.MarkUnknown(send.auth, unknownAmbiguous, time.Now().Add(finalizeFirstRetry))
	}
}

// refusalCode is the provider's code as the failure stored for the operator,
// within the column; the client never sees it.
func refusalCode(refused *ramp.FinalizeRefusedError) string {
	code := refused.Code
	if code == "" {
		return refusedWithoutCode
	}
	if len(code) > failureCodeMaxLength {
		return code[:failureCodeMaxLength]
	}
	return code
}
