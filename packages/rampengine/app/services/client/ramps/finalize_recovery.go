package ramps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	// finalizeRecoveryMaxAttempts is how many recovery attempts a finalize
	// gets before it goes to an operator (plan D8). A constant, not a setting.
	finalizeRecoveryMaxAttempts = 8
	// finalizeRecoveryMaxBackoff caps the wait between attempts.
	finalizeRecoveryMaxBackoff = 30 * time.Minute
	// providerReplayWindow is how long the provider replays a create under
	// its Idempotency-Key: within it the recovery sends the same bytes again,
	// past it it only looks the payout up by its quote.
	providerReplayWindow = 24 * time.Hour
	// quoteLapseGrace is how long past the stored quote expiry "not created"
	// waits: it covers clock skew with the provider and the now+5min expiry
	// the adapter assumes when a quote states none.
	quoteLapseGrace = 10 * time.Minute

	// The codes an inconclusive pass is logged with (MarkUnknown and Escalate
	// do not store them).
	unknownLookupFailed       = "payout_lookup_failed"
	unknownQuoteLive          = "payout_not_found_quote_live"
	unknownNotRecorded        = "finalize_outcome_not_recorded"
	unknownNoProvider         = "provider_unavailable"
	unknownOffBindingUnlisted = "payout_off_binding_not_listed"
	escalateExhausted         = "recovery_attempts_exhausted"
	escalateMismatch          = "binding_mismatch"
	escalateBindingUnreadable = "binding_unreadable"
)

// The refusals of Resolve. ErrBindingMismatch aliases the adapter's sentinel,
// so errors.Is holds through the service's wrapping.
var (
	// ErrFinalizationNotFound is an authorization id that names none.
	ErrFinalizationNotFound = errors.New("ramps: finalization not found")
	// ErrFinalizationNotResolvable is an authorization that is not unknown
	// or held for an operator, or that a recovery holds right now.
	ErrFinalizationNotResolvable = errors.New("ramps: finalization is not open to a resolution")
	// ErrInvalidFinalizationResolution is a verdict that is not exactly one of
	// a payout id or "not created".
	ErrInvalidFinalizationResolution = errors.New("ramps: a resolution is a payout id or not_created, exactly one")
	// ErrQuoteStillValid refuses "not created" while the quote could still
	// be redeemed: the payout may yet be created from it.
	ErrQuoteStillValid = errors.New("ramps: the quote is still valid")
	// ErrPayoutExists refuses "not created" when the provider's listing shows
	// a payout for the quote: it is resolved with that payout's id instead.
	ErrPayoutExists = errors.New("ramps: the provider holds a payout for this quote")
	// ErrBindingMismatch is a payout that is not the one the binding
	// describes; nothing is bound.
	ErrBindingMismatch = ramp.ErrBindingMismatch
)

// FinalizationRecovery closes the bank payout finalizes the send left open
// (spec §5.8 item 4, plan D8): a row still finalizing past its lease (the
// sender died, or never sent) and an unknown outcome due again. It is the
// only path that sends a finalize twice, and only ever the same bytes under
// the same provider key, so the provider replays rather than pays again.
type FinalizationRecovery struct {
	engine
	now func() time.Time
}

// NewFinalizationRecovery builds the recovery from the ramp collaborators.
func NewFinalizationRecovery(d Deps) *FinalizationRecovery {
	return &FinalizationRecovery{engine: newEngine(d), now: time.Now}
}

// WithClock answers a copy of the recovery reading now from clock: which rows
// are due, the replay window and the quote's expiry.
func (r *FinalizationRecovery) WithClock(clock func() time.Time) *FinalizationRecovery {
	cp := *r
	cp.now = clock
	return &cp
}

// RecoveryReport is what one sweep did.
type RecoveryReport struct {
	// Due is what the listing returned; Claimed what this sweep leased.
	Due, Claimed int
	// Finalized bound a payout, Failed closed a finalize no payout came of,
	// Rescheduled left it unknown for a later attempt, Escalated handed it
	// to an operator.
	Finalized, Failed, Rescheduled, Escalated int
	// Errors are rows whose pass could not be written; they stay leased and
	// are due again when the lease runs out.
	Errors int
}

// verdict is what one pass concluded about an authorization.
type verdict struct {
	kind     verdictKind
	payout   *ramp.RampOrder
	failCode string
	toStatus string
	// code is the reason of an unknown or an escalation, for the log.
	code string
}

type verdictKind int

const (
	verdictUnknown verdictKind = iota
	verdictFinalized
	verdictFailed
	verdictEscalate
)

func unknownBecause(code string) verdict { return verdict{kind: verdictUnknown, code: code} }

// Recover works through the authorizations due for recovery, oldest first,
// up to limit. Each is claimed by a compare-and-swap on its lease, which
// counts the attempt; a claim lost to another recovery is skipped. Only a
// listing that fails fails the sweep: a row whose pass could not be written
// is counted in Errors and left to its lease.
func (r *FinalizationRecovery) Recover(ctx context.Context, limit int) (RecoveryReport, error) {
	var report RecoveryReport
	due, err := r.funding.ListDueForRecovery(r.now(), limit)
	if err != nil {
		return report, fmt.Errorf("ramps: list finalizations due for recovery: %w", err)
	}
	report.Due = len(due)
	for i := range due {
		if err := ctx.Err(); err != nil {
			// Nothing of the rest is claimed: each stays due for the next sweep.
			return report, err
		}
		r.recoverOne(ctx, &due[i], &report)
	}
	return report, nil
}

// recoverOne claims and acts on one authorization.
func (r *FinalizationRecovery) recoverOne(ctx context.Context, auth *models.OfframpFundingAuthorization, report *RecoveryReport) {
	log := facades.Log().WithContext(ctx).With(map[string]any{"authorization": auth.ExternalID})
	now := r.now()
	claimed, err := r.funding.ClaimRecovery(auth, now, now.Add(finalizeLease))
	if err != nil {
		report.Errors++
		log.Errorf("ramps: recovery: claim: %v", err)
		return
	}
	if !claimed {
		return
	}
	report.Claimed++
	tx, err := r.txRepo.FindByID(auth.TransactionID)
	if err != nil || tx == nil {
		report.Errors++
		log.Errorf("ramps: recovery: load order %d: %v", auth.TransactionID, err)
		return
	}
	var v verdict
	if auth.RecoveryAttempts > finalizeRecoveryMaxAttempts {
		// Already past its attempts: an escalation that did not land before.
		v = verdict{kind: verdictEscalate, code: escalateExhausted}
	} else {
		v = r.decide(ctx, auth, tx)
	}
	r.apply(ctx, auth, tx, v, report)
}

// decide is one pass of the spec's table: send the same bytes again within the
// provider's replay window, otherwise (or when the send says the quote is
// spent, replays nothing, or names a payout it cannot bind) look the payout up
// by its quote. A resend that named a payout is evidence one was created, so
// the lookup not finding it never closes the finalize as failed.
func (r *FinalizationRecovery) decide(ctx context.Context, auth *models.OfframpFundingAuthorization, tx *models.Transaction) verdict {
	binding, err := storedBinding(auth)
	if err != nil {
		return verdict{kind: verdictEscalate, code: escalateBindingUnreadable}
	}
	adapter, ref, err := r.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return unknownBecause(unknownNoProvider)
	}
	sender, ok := adapter.(ramp.OfframpFinalizeAdapter)
	if !ok {
		return unknownBecause(unknownNoProvider)
	}
	payoutNamed := false
	if r.withinReplayWindow(auth) {
		if signed, ok := r.signedBytes(auth); ok {
			var (
				v       verdict
				settled bool
			)
			v, settled, payoutNamed = r.resend(ctx, sender, ref, auth, binding, signed)
			if settled {
				return v
			}
		}
	}
	v := r.lookup(ctx, sender, ref, tx, binding)
	if payoutNamed && v.kind == verdictFailed {
		return unknownBecause(unknownOffBindingUnlisted)
	}
	return v
}

func (r *FinalizationRecovery) withinReplayWindow(auth *models.OfframpFundingAuthorization) bool {
	return auth.FinalizeStartedAt != nil && r.now().Sub(*auth.FinalizeStartedAt) < providerReplayWindow
}

// signedBytes opens the stored signed transaction; false when there is none
// to send (it cannot be decrypted, or was never stored).
func (r *FinalizationRecovery) signedBytes(auth *models.OfframpFundingAuthorization) (string, bool) {
	if auth.SignedPayloadCiphertext == nil || auth.ProviderFinalizeKey == nil || *auth.ProviderFinalizeKey == "" {
		return "", false
	}
	signed, err := facades.Crypt().DecryptString(*auth.SignedPayloadCiphertext)
	if err != nil || signed == "" {
		return "", false
	}
	return signed, true
}

// resend sends the intent again, bounded like the finalize and not cut short
// by the sweep's own deadline. It reports whether the answer settles the pass,
// and whether it named a payout it could not bind; a spent quote, a lapsed
// one, a refusal, a 200 naming no payout and a payout off the binding go to
// the lookup. Only the marks and a payout are read: anything else, an HTTP
// status included, is ambiguous.
func (r *FinalizationRecovery) resend(
	ctx context.Context, sender ramp.OfframpFinalizeAdapter, ref ramp.CustomerRef,
	auth *models.OfframpFundingAuthorization, binding ramp.OrderBinding, signed string,
) (v verdict, settled, payoutNamed bool) {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeSendTimeout)
	order, err := sender.FinalizeOfframp(sendCtx, ref, ramp.FinalizeRequest{
		OrderBinding: binding, SignedTransaction: signed, IdempotencyKey: *auth.ProviderFinalizeKey,
	})
	cancel()
	switch {
	case err == nil && order != nil && order.ProviderOrderID != "":
		return verdict{kind: verdictFinalized, payout: order}, true, false
	case err == nil:
		return verdict{}, false, false
	case errors.Is(err, ramp.ErrBindingMismatch):
		// The replay names a payout this side cannot bind from the answer: the
		// listing, which names the whole binding, binds it or shows the
		// mismatch.
		return verdict{}, false, true
	case errors.Is(err, ramp.ErrQuoteAlreadyUsed), errors.Is(err, ramp.ErrEstimateExpired), isRefused(err):
		// A refusal of the resend proves only that the resend created nothing:
		// the first request may have created the payout. The lookup decides.
		return verdict{}, false, false
	default:
		return unknownBecause(unknownAmbiguous), true, false
	}
}

// lookup finds the payout by its quote. Found is bound (the adapter held it to
// the binding); one off the binding goes to an operator. Not found closes the
// finalize only when the listing answered and the quote has lapsed: no payout
// can be created from it any more.
func (r *FinalizationRecovery) lookup(
	ctx context.Context, sender ramp.OfframpFinalizeAdapter, ref ramp.CustomerRef, tx *models.Transaction, binding ramp.OrderBinding,
) verdict {
	findCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeSendTimeout)
	order, err := sender.FindOfframpByQuote(findCtx, ref, ramp.FindByQuoteRequest{OrderBinding: binding})
	cancel()
	switch {
	case errors.Is(err, ramp.ErrBindingMismatch):
		return verdict{kind: verdictEscalate, code: escalateMismatch}
	case err != nil:
		return unknownBecause(unknownLookupFailed)
	case order != nil && order.ProviderOrderID != "":
		return verdict{kind: verdictFinalized, payout: order}
	case order != nil:
		return unknownBecause(unknownLookupFailed)
	case r.quoteLapsed(tx):
		return verdict{kind: verdictFailed, failCode: models.FundingFailurePayoutNotCreated, toStatus: models.TxStatusFailed}
	default:
		return unknownBecause(unknownQuoteLive)
	}
}

// quoteLapsed reports whether the order's quote expired by the clock, more
// than quoteLapseGrace ago. A quote that cannot be read is taken as live:
// that direction concludes nothing.
func (r *FinalizationRecovery) quoteLapsed(tx *models.Transaction) bool {
	if tx.EstimateID == nil {
		return false
	}
	estimate, err := r.estimateRepo.FindByCustomerAndID(tx.CustomerID, *tx.EstimateID)
	if err != nil || estimate == nil || estimate.ExpiresAt == nil {
		return false
	}
	return r.now().After(estimate.ExpiresAt.Add(quoteLapseGrace))
}

// apply writes the verdict by its repository transition. A write that fails is
// never taken for success: the row is made unknown for a later attempt, and a
// pass whose writes all fail leaves it to its lease, due again with the next
// attempt counted, until the attempts run out.
func (r *FinalizationRecovery) apply(
	ctx context.Context, auth *models.OfframpFundingAuthorization, tx *models.Transaction, v verdict, report *RecoveryReport,
) {
	log := facades.Log().WithContext(ctx).With(map[string]any{"authorization": auth.ExternalID, "order": tx.ExternalID})
	before := tx.Status
	var (
		won bool
		err error
	)
	switch v.kind {
	case verdictFinalized:
		won, err = r.funding.CompleteFinalized(auth, v.payout.ProviderOrderID, v.payout.TxHash)
		if err == nil && won {
			report.Finalized++
			log.Infof("ramps: recovery bound payout %s", v.payout.ProviderOrderID)
		}
	case verdictFailed:
		won, err = r.funding.CompleteFailed(auth, v.failCode, v.toStatus)
		if err == nil && won {
			report.Failed++
			log.Infof("ramps: recovery closed the finalize as failed: %s", v.failCode)
		}
	case verdictEscalate:
		r.escalate(ctx, auth, tx, v.code, report)
		r.announceIfMoved(tx, before)
		return
	default:
		r.reschedule(ctx, auth, tx, v.code, report)
		r.announceIfMoved(tx, before)
		return
	}
	if err != nil {
		log.Errorf("ramp.attention: recovery outcome not written, retried: %v", err)
		report.Errors++
		r.reschedule(ctx, auth, tx, unknownNotRecorded, nil)
		return
	}
	if won {
		r.announceIfMoved(tx, "")
	}
}

// reschedule leaves the row unknown for its next attempt, after
// min(2^attempts minutes, 30 minutes) — or escalates it when that was its last.
// A nil report counts nothing (the caller already counted the pass).
func (r *FinalizationRecovery) reschedule(
	ctx context.Context, auth *models.OfframpFundingAuthorization, tx *models.Transaction, code string, report *RecoveryReport,
) {
	if auth.RecoveryAttempts >= finalizeRecoveryMaxAttempts {
		r.escalate(ctx, auth, tx, escalateExhausted, report)
		return
	}
	log := facades.Log().WithContext(ctx).With(map[string]any{"authorization": auth.ExternalID})
	won, err := r.funding.MarkUnknown(auth, code, r.now().Add(recoveryBackoff(auth.RecoveryAttempts)))
	switch {
	case err != nil:
		log.Errorf("ramps: recovery: reschedule (%s) not written, left to its lease: %v", code, err)
		if report != nil {
			report.Errors++
		}
	case won && report != nil:
		report.Rescheduled++
		log.Warningf("ramps: recovery inconclusive (%s), attempt %d", code, auth.RecoveryAttempts)
	}
}

// recoveryBackoff is min(2^attempts minutes, 30 minutes).
func recoveryBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts >= 5 { // 2^5 minutes is past the cap
		return finalizeRecoveryMaxBackoff
	}
	return time.Duration(1<<attempts) * time.Minute
}

// escalate hands the row to an operator, with the ramp.attention line the
// queue is watched by (ids and timestamps only). A finalizing row is made
// unknown first, so its order moves to processing (the money may be moving);
// when that cannot be written — its order was moved where no finalize write
// can move it from — the row is escalated directly, writing no order, so it
// stops being re-claimed and surfaces in the operator queue.
func (r *FinalizationRecovery) escalate(
	ctx context.Context, auth *models.OfframpFundingAuthorization, tx *models.Transaction, code string, report *RecoveryReport,
) {
	fields := map[string]any{
		"authorization": auth.ExternalID, "order": tx.ExternalID, "attempts": auth.RecoveryAttempts, "code": code,
	}
	if auth.FinalizeStartedAt != nil {
		fields["finalize_started_at"] = auth.FinalizeStartedAt.UTC().Format(time.RFC3339)
	}
	log := facades.Log().WithContext(ctx).With(fields)
	if code == escalateMismatch {
		r.markBindingMismatch(ctx, tx)
	}
	if auth.State != models.FundingAuthUnknown {
		if _, err := r.funding.MarkUnknown(auth, code, r.now().Add(finalizeRecoveryMaxBackoff)); err != nil {
			log.Errorf("ramp.attention: finalization could not be made unknown, escalating it directly: %v", err)
		}
	}
	won, err := r.funding.Escalate(auth, code)
	if err != nil || !won {
		log.Errorf("ramp.attention: finalization could not be handed to an operator (won=%t): %v", won, err)
		if report != nil {
			report.Errors++
		}
		return
	}
	if report != nil {
		report.Escalated++
	}
	log.Errorf("ramp.attention: finalization handed to an operator")
}

// announceIfMoved tells the order's audience, and its devices, when its status
// moved from before ("" announces any landed write).
func (r *FinalizationRecovery) announceIfMoved(tx *models.Transaction, before string) {
	current, err := r.txRepo.FindByID(tx.ID)
	if err != nil || current == nil {
		facades.Log().Warningf("ramps: recovery: reload order %s: %v", tx.ExternalID, err)
		return
	}
	if before != "" && current.Status == before {
		return
	}
	r.publish(current)
	if r.notifier != nil {
		r.notifier.TransactionUpdated(current)
	}
}

// storedBinding decodes payout_binding, written by Finalize from the same
// struct (no json tags: the keys are the Go field names).
func storedBinding(auth *models.OfframpFundingAuthorization) (ramp.OrderBinding, error) {
	var binding ramp.OrderBinding
	if auth.PayoutBinding == nil || *auth.PayoutBinding == "" {
		return binding, errors.New("ramps: the authorization has no payout binding")
	}
	if err := json.Unmarshal([]byte(*auth.PayoutBinding), &binding); err != nil {
		return binding, fmt.Errorf("ramps: decode payout binding: %w", err)
	}
	if binding.QuoteID == "" || binding.ProviderCustomerID == "" || binding.BankAccountID == "" {
		return binding, errors.New("ramps: the payout binding is incomplete")
	}
	return binding, nil
}

// Resolve closes an unknown or operator-held finalize with an operator's
// verdict (E8), by the repository's operator transitions:
//
//   - a payout id is read by GetBoundOrder against the stored binding and
//     bound only when it matches; otherwise ErrBindingMismatch, and an
//     unknown row goes to an operator;
//   - "not created" is taken only once the quote has lapsed
//     (ErrQuoteStillValid) and the provider's listing answers without a
//     payout for it (ErrPayoutExists when it shows one).
//
// A row a recovery holds right now is not resolvable: the two would race.
func (r *FinalizationRecovery) Resolve(ctx context.Context, authExternalID string, in dto.ResolveFinalizationDTO) error {
	in.PayoutID = strings.TrimSpace(in.PayoutID)
	if (in.PayoutID == "") == !in.NotCreated {
		return ErrInvalidFinalizationResolution
	}
	auth, err := r.funding.FindByExternalID(authExternalID)
	if err != nil {
		return fmt.Errorf("ramps: load finalization: %w", err)
	}
	if auth == nil {
		return ErrFinalizationNotFound
	}
	now := r.now()
	leased := auth.FinalizeLeaseExpiresAt != nil && auth.FinalizeLeaseExpiresAt.After(now)
	if (auth.State != models.FundingAuthUnknown && auth.State != models.FundingAuthNeedsOperator) || leased {
		return ErrFinalizationNotResolvable
	}
	tx, err := r.txRepo.FindByID(auth.TransactionID)
	if err != nil {
		return fmt.Errorf("ramps: load order of finalization: %w", err)
	}
	if tx == nil {
		return ErrFinalizationNotFound
	}
	binding, err := storedBinding(auth)
	if err != nil {
		return err
	}
	adapter, ref, err := r.router.ResolveForSettlement(ctx, tx.CustomerID, tx.Provider)
	if err != nil {
		return fmt.Errorf("ramps: resolve provider of finalization: %w", err)
	}
	if in.NotCreated {
		return r.resolveNotCreated(ctx, adapter, ref, auth, tx, binding)
	}
	return r.resolvePayout(ctx, adapter, ref, auth, tx, binding, in.PayoutID)
}

func (r *FinalizationRecovery) resolvePayout(
	ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef,
	auth *models.OfframpFundingAuthorization, tx *models.Transaction, binding ramp.OrderBinding, payoutID string,
) error {
	reader, ok := adapter.(ramp.BoundOrderReader)
	if !ok {
		return fmt.Errorf("ramps: %s cannot read a bound payout: %w", tx.Provider, ramp.ErrProviderNotImplemented)
	}
	order, err := reader.GetBoundOrder(ctx, ref, payoutID, binding)
	if err == nil && (order == nil || order.ProviderOrderID != payoutID) {
		err = ramp.ErrBindingMismatch
	}
	if errors.Is(err, ramp.ErrBindingMismatch) {
		r.refuseMismatch(ctx, auth, tx)
		return fmt.Errorf("ramps: resolve finalization with payout %s: %w", payoutID, ErrBindingMismatch)
	}
	if err != nil {
		return fmt.Errorf("ramps: read payout %s: %w", payoutID, err)
	}
	won, err := r.funding.ResolveFinalized(auth, payoutID, order.TxHash)
	if err != nil {
		return fmt.Errorf("ramps: resolve finalization: %w", err)
	}
	if !won {
		return ErrFinalizationNotResolvable
	}
	r.announceIfMoved(tx, "")
	return nil
}

func (r *FinalizationRecovery) resolveNotCreated(
	ctx context.Context, adapter ramp.ProviderAdapter, ref ramp.CustomerRef,
	auth *models.OfframpFundingAuthorization, tx *models.Transaction, binding ramp.OrderBinding,
) error {
	if !r.quoteLapsed(tx) {
		return ErrQuoteStillValid
	}
	sender, ok := adapter.(ramp.OfframpFinalizeAdapter)
	if !ok {
		return fmt.Errorf("ramps: %s cannot look a payout up: %w", tx.Provider, ramp.ErrProviderNotImplemented)
	}
	order, err := sender.FindOfframpByQuote(ctx, ref, ramp.FindByQuoteRequest{OrderBinding: binding})
	if errors.Is(err, ramp.ErrBindingMismatch) {
		r.refuseMismatch(ctx, auth, tx)
		return fmt.Errorf("ramps: resolve finalization as not created: %w", ErrBindingMismatch)
	}
	if err != nil {
		return fmt.Errorf("ramps: look up the payout of the quote: %w", err)
	}
	if order != nil {
		return ErrPayoutExists
	}
	won, err := r.funding.ResolveNotCreated(auth)
	if err != nil {
		return fmt.Errorf("ramps: resolve finalization: %w", err)
	}
	if !won {
		return ErrFinalizationNotResolvable
	}
	r.announceIfMoved(tx, "")
	return nil
}

// refuseMismatch logs a provider payout off the binding and hands an unknown
// row to an operator; nothing is bound.
func (r *FinalizationRecovery) refuseMismatch(ctx context.Context, auth *models.OfframpFundingAuthorization, tx *models.Transaction) {
	facades.Log().WithContext(ctx).With(map[string]any{"authorization": auth.ExternalID, "order": tx.ExternalID}).
		Errorf("ramp.attention: a resolution named a payout that does not match the binding")
	if auth.State == models.FundingAuthUnknown {
		r.escalate(ctx, auth, tx, escalateMismatch, nil)
		return
	}
	// A row already with an operator is not escalated again, but the order
	// still carries the mark the queue reads.
	r.markBindingMismatch(ctx, tx)
}

// markBindingMismatch raises the attention mark of the authorization's order
// before it is escalated. A failure is logged and never fails the pass: the
// escalation is what hands the row to an operator.
func (r *FinalizationRecovery) markBindingMismatch(ctx context.Context, tx *models.Transaction) {
	if _, err := r.txRepo.MarkAttention(tx.ID, models.AttentionBindingMismatch, r.now()); err != nil {
		facades.Log().WithContext(ctx).With(map[string]any{"order": tx.ExternalID}).
			Errorf("ramp.attention: mark order as not matching its binding: %v", err)
	}
}

// PurgePayloads clears the signed bytes of closed finalizes whose retention
// ended, up to limit, and reports how many.
func (r *FinalizationRecovery) PurgePayloads(now time.Time, limit int) (int64, error) {
	n, err := r.funding.ClearRetainedPayloads(now, limit)
	if err != nil {
		return 0, fmt.Errorf("ramps: purge signed payloads: %w", err)
	}
	return n, nil
}

// isRefused reports a definitive refusal from the provider.
func isRefused(err error) bool {
	var refused *ramp.FinalizeRefusedError
	return errors.As(err, &refused)
}
