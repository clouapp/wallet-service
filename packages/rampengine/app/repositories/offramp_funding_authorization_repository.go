package repositories

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

type OfframpFundingAuthorizationRepository interface {
	// FindByTransaction is (nil, nil) when the transaction has none.
	FindByTransaction(txID uint) (*models.OfframpFundingAuthorization, error)
	// FindByExternalID is (nil, nil) when no authorization has the id.
	FindByExternalID(externalID string) (*models.OfframpFundingAuthorization, error)
	// ReplaceAction swaps the prepared action for a fresh one, only while the
	// row is still prepared at previousRevision, and reports whether it did.
	// On success a carries the new revision.
	ReplaceAction(a *models.OfframpFundingAuthorization, previousRevision int) (bool, error)
	// CreatePreparedConsumingEstimate stores the authorization in the prepared
	// state, consumes the estimate and makes the authorization's transaction
	// await its funds until fundableUntil, in one transaction: a quote is spent
	// exactly when its authorization exists, and the order is fundable exactly
	// as long. It fails, writing nothing, unless the transaction is still
	// pending; a second authorization for the same transaction violates the
	// unique and fails too.
	CreatePreparedConsumingEstimate(a *models.OfframpFundingAuthorization, estimateID uint, fundableUntil time.Time) error
	// FailAndExpire fails a prepared authorization and expires its transaction
	// (only while pending or awaiting_funds) in one transaction. It reports
	// whether the authorization changed; when it did not, nothing is written.
	FailAndExpire(a *models.OfframpFundingAuthorization, code string) (bool, error)

	// BeginFinalize moves prepared@revision to finalizing and writes the intent.
	BeginFinalize(a *models.OfframpFundingAuthorization, revision int, in FinalizeIntent) (bool, error)
	// CompleteFinalized: finalizing|unknown → finalized; the transaction becomes
	// processing with the provider's payout id (and the collection hash, if any).
	CompleteFinalized(a *models.OfframpFundingAuthorization, providerOrderID, txHash string) (bool, error)
	// CompleteFailed: finalizing|unknown → failed with code, and the transaction
	// ends in toStatus (failed, or expired for a lapsed quote) in the same write.
	CompleteFailed(a *models.OfframpFundingAuthorization, code, toStatus string) (bool, error)
	// MarkUnknown: finalizing|unknown → unknown (an unknown row is rescheduled),
	// transaction → processing, releases the lease and schedules the next
	// attempt. The code is required but not stored: failure_code is reserved
	// for the failed state by a CHECK.
	MarkUnknown(a *models.OfframpFundingAuthorization, code string, next time.Time) (bool, error)
	// ClaimRecovery takes the lease of one row due for recovery and counts the attempt.
	ClaimRecovery(a *models.OfframpFundingAuthorization, now, leaseUntil time.Time) (bool, error)
	// Escalate: finalizing|unknown → needs_operator, writing no order (a
	// finalizing row's order may sit where no finalize write can move it from,
	// and the operator queue is where it must surface). Like MarkUnknown, the
	// code is required for the caller's log and not stored.
	Escalate(a *models.OfframpFundingAuthorization, code string) (bool, error)
	// ResolveFinalized is an operator's verdict that the payout exists:
	// unknown|needs_operator → finalized, the order as CompleteFinalized moves
	// it. A finalizing row is still the automation's and loses.
	ResolveFinalized(a *models.OfframpFundingAuthorization, providerOrderID, txHash string) (bool, error)
	// ResolveNotCreated is an operator's verdict that the payout was never
	// created: unknown|needs_operator → failed (payout_not_created), and the
	// order failed in the same write.
	ResolveNotCreated(a *models.OfframpFundingAuthorization) (bool, error)
	// ListDueForRecovery: finalizing with an expired lease, or unknown due now, oldest first.
	ListDueForRecovery(now time.Time, limit int) ([]models.OfframpFundingAuthorization, error)
	// ListNeedingOperator returns up to limit authorizations in needs_operator,
	// oldest updated_at first, each with its transaction.
	ListNeedingOperator(limit int) ([]FundingAuthorizationWithTransaction, error)
	// ClearRetainedPayloads nulls the signed payload of terminal authorizations
	// whose retention ended, and reports how many.
	ClearRetainedPayloads(now time.Time, limit int) (int64, error)
}

// FundingAuthorizationWithTransaction pairs an authorization with the
// transaction it funds.
type FundingAuthorizationWithTransaction struct {
	Authorization models.OfframpFundingAuthorization
	Transaction   models.Transaction
}

// FinalizeIntent is what BeginFinalize writes.
type FinalizeIntent struct {
	ClientKeyHash, RequestHash, ProviderKey string
	SignedPayloadCiphertext                 string
	PayoutBinding                           string
	StartedAt, LeaseUntil, RetainUntil      time.Time
}

type offrampFundingAuthorizationRepository struct {
	tenant, environment string
	customerID          uint
}

func NewOfframpFundingAuthorizationRepository() OfframpFundingAuthorizationRepository {
	return &offrampFundingAuthorizationRepository{}
}

func createPrepared(q orm.Query, a *models.OfframpFundingAuthorization) error {
	if a.ExternalID == "" {
		a.ExternalID = uuid.NewString()
	}
	a.State = models.FundingAuthPrepared
	if a.ActionRevision == 0 {
		a.ActionRevision = 1
	}
	a.FailureCode = nil
	if err := q.Create(a); err != nil {
		return fmt.Errorf("funding authorization repo: create: %w", err)
	}
	return nil
}

// failPrepared fails the authorization whose column equals value, only while
// it is prepared, and reports whether it did.
func failPrepared(q orm.Query, column string, value uint, code string) (bool, error) {
	if code == "" {
		return false, errors.New("funding authorization repo: fail: empty failure code")
	}
	result, err := q.Model(&models.OfframpFundingAuthorization{}).
		Where(column, value).Where("state", models.FundingAuthPrepared).
		Update(map[string]any{"state": models.FundingAuthFailed, "failure_code": code})
	if err != nil {
		return false, fmt.Errorf("funding authorization repo: fail: %w", err)
	}
	return result.RowsAffected > 0, nil
}

// unfundedStatuses are the statuses an order expires from.
var unfundedStatuses = []string{models.TxStatusPending, models.TxStatusAwaitingFunds}

// lockTransaction takes the transaction's row lock. Every write that touches
// a transaction and its funding authorization together takes it first, so
// two of them always lock in the same order and never deadlock, and a create
// recording an authorization for the row (its foreign key share-locks it)
// waits for the write to commit.
func lockTransaction(q orm.Query, id uint) (*models.Transaction, error) {
	return first[models.Transaction](q.LockForUpdate().Where("id", id), "funding authorization repo: lock transaction")
}

// expireUnfunded expires the transaction only while it is unfunded and no
// deposit has claimed it.
func expireUnfunded(q orm.Query, txID uint) (bool, error) {
	result, err := q.Model(&models.Transaction{}).
		Where("id", txID).
		Where("status IN ?", unfundedStatuses).
		Where("NOT ("+claimedOzavOfframp+")", models.ProviderOzav, models.TransactionTypeOfframp).
		Update("status", models.TxStatusExpired)
	if err != nil {
		return false, fmt.Errorf("expire transaction: %w", err)
	}
	return result.RowsAffected > 0, nil
}

// expireUnfundedFailingPrepared is the local expiry of an unfunded order,
// inside q's transaction. A prepared funding authorization dies with the
// order, failed with code. An authorization in any other state means the
// funding may be under way (or already settled), so the order is left alone
// and nothing is written; so is an Ozav off-ramp a deposit has claimed. An
// order with no authorization (Pix) just expires.
func expireUnfundedFailingPrepared(q orm.Query, txID uint, code string) (bool, error) {
	tx, err := lockTransaction(q, txID)
	if err != nil || tx == nil {
		return false, err
	}
	if !slices.Contains(unfundedStatuses, tx.Status) || depositClaimed(tx) {
		return false, nil
	}
	failed, err := failPrepared(q, "transaction_id", txID, code)
	if err != nil {
		return false, err
	}
	if !failed {
		others, err := q.Model(&models.OfframpFundingAuthorization{}).Where("transaction_id", txID).Count()
		if err != nil {
			return false, fmt.Errorf("funding authorization repo: count authorizations: %w", err)
		}
		if others > 0 {
			return false, nil
		}
	}
	return expireUnfunded(q, txID)
}

// depositClaimed reports whether a deposit has claimed tx, an Ozav off-ramp.
func depositClaimed(tx *models.Transaction) bool {
	return tx.Provider == models.ProviderOzav && tx.Type == models.TransactionTypeOfframp &&
		tx.DepositTxSignature != nil && strings.TrimSpace(*tx.DepositTxSignature) != ""
}

func (r *offrampFundingAuthorizationRepository) FindByTransaction(txID uint) (*models.OfframpFundingAuthorization, error) {
	return first[models.OfframpFundingAuthorization](facades.Orm().Query().
		Where("transaction_id", txID), "funding authorization repo: find by transaction")
}

func (r *offrampFundingAuthorizationRepository) FindByExternalID(externalID string) (*models.OfframpFundingAuthorization, error) {
	// The column is a uuid: anything else cannot name a row, and would be a
	// database error rather than a miss.
	if _, err := uuid.Parse(externalID); err != nil {
		return nil, nil
	}
	return first[models.OfframpFundingAuthorization](facades.Orm().Query().
		Where("external_id", externalID), "funding authorization repo: find by external id")
}

func (r *offrampFundingAuthorizationRepository) ReplaceAction(a *models.OfframpFundingAuthorization, previousRevision int) (bool, error) {
	result, err := facades.Orm().Query().Model(&models.OfframpFundingAuthorization{}).
		Where("id", a.ID).
		Where("state", models.FundingAuthPrepared).
		Where("action_revision", previousRevision).
		Update(map[string]any{
			"provider_state_ciphertext":   a.ProviderStateCiphertext,
			"action_payload_ciphertext":   a.ActionPayloadCiphertext,
			"unsigned_action_fingerprint": a.UnsignedActionFingerprint,
			"expires_at":                  a.ExpiresAt,
			"action_revision":             previousRevision + 1,
		})
	if err != nil {
		return false, fmt.Errorf("funding authorization repo: replace action: %w", err)
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	a.ActionRevision = previousRevision + 1
	return true, nil
}

func (r *offrampFundingAuthorizationRepository) CreatePreparedConsumingEstimate(
	a *models.OfframpFundingAuthorization, estimateID uint, fundableUntil time.Time,
) error {
	return facades.Orm().Transaction(func(tx orm.Query) error {
		if err := createPrepared(tx, a); err != nil {
			return err
		}
		// Only a reservation still pending becomes fundable: one the reconciler
		// expired, or another create already settled, while this prepare ran
		// must not be resurrected.
		updated, err := tx.Model(&models.Transaction{}).Where("id", a.TransactionID).
			Where("status", models.TxStatusPending).
			Update(map[string]any{"status": models.TxStatusAwaitingFunds, "expires_at": fundableUntil})
		if err != nil {
			return fmt.Errorf("funding authorization repo: extend transaction: %w", err)
		}
		if updated.RowsAffected == 0 {
			return fmt.Errorf("funding authorization repo: transaction %d is missing or no longer pending", a.TransactionID)
		}
		result, err := tx.Model(&models.RampEstimate{}).
			Where("id", estimateID).Where("consumed_at IS NULL").
			Update("consumed_at", time.Now())
		if err != nil {
			return fmt.Errorf("funding authorization repo: consume estimate: %w", err)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("funding authorization repo: estimate %d is already consumed or missing", estimateID)
		}
		return nil
	})
}

func (r *offrampFundingAuthorizationRepository) FailAndExpire(a *models.OfframpFundingAuthorization, code string) (bool, error) {
	changed := false
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		if _, err := lockTransaction(tx, a.TransactionID); err != nil {
			return err
		}
		ok, err := failPrepared(tx, "id", a.ID, code)
		if err != nil || !ok {
			return err
		}
		changed = true
		if _, err := expireUnfunded(tx, a.TransactionID); err != nil {
			return fmt.Errorf("funding authorization repo: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if changed {
		a.State, a.FailureCode = models.FundingAuthFailed, &code
	}
	return changed, nil
}

// movingStatuses are the statuses an order is in while its payout may be
// created or in flight; a finalize outcome writes the order only from them.
var movingStatuses = []string{models.TxStatusPending, models.TxStatusAwaitingFunds, models.TxStatusProcessing}

// dueForRecovery selects the rows recovery owns: finalizing with its lease
// gone, or unknown whose next attempt came and whose lease is free.
const dueForRecovery = "((state = 'finalizing' AND (finalize_lease_expires_at IS NULL OR finalize_lease_expires_at <= ?)) OR " +
	"(state = 'unknown' AND (recovery_next_attempt_at IS NULL OR recovery_next_attempt_at <= ?) AND " +
	"(finalize_lease_expires_at IS NULL OR finalize_lease_expires_at <= ?)))"

// finalizeTransition runs one finalize write in its own transaction. It locks
// the order first (the order FailAndExpire takes), then calls write, which
// compare-and-swaps the authorization and returns false when it lost. A lost
// swap writes nothing; a won one runs onWin against the same transaction.
func finalizeTransition(
	a *models.OfframpFundingAuthorization,
	what string,
	write func(q orm.Query) (bool, error),
	onWin func(q orm.Query) error,
) (bool, error) {
	won := false
	err := facades.Orm().Transaction(func(q orm.Query) error {
		tx, err := lockTransaction(q, a.TransactionID)
		if err != nil {
			return err
		}
		if tx == nil {
			return nil
		}
		ok, err := write(q)
		if err != nil || !ok {
			return err
		}
		if onWin != nil {
			if err := onWin(q); err != nil {
				return err
			}
		}
		won = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("funding authorization repo: %s: %w", what, err)
	}
	return won, nil
}

// swap updates the authorization a only while its state is one of from.
func swap(q orm.Query, a *models.OfframpFundingAuthorization, from []string, set map[string]any) (bool, error) {
	result, err := q.Model(&models.OfframpFundingAuthorization{}).
		Where("id", a.ID).Where("state IN ?", from).Update(set)
	if err != nil {
		return false, err
	}
	return result.RowsAffected > 0, nil
}

// ErrOrderNotMovable is a finalize write whose order another path already took
// out of a moving status (expired, failed, completed): the authorization's
// write rolled back with it.
var ErrOrderNotMovable = errors.New("the order is not in a status a finalize can move")

// moveOrder updates the order of a funding authorization, only from a moving
// status. Failing to is an error: the authorization already moved, and the
// write rolls back so recovery or an operator sees a row that still needs them.
func moveOrder(q orm.Query, txID uint, set map[string]any) error {
	result, err := q.Model(&models.Transaction{}).
		Where("id", txID).Where("status IN ?", movingStatuses).Update(set)
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("transaction %d: %w", txID, ErrOrderNotMovable)
	}
	return nil
}

func (r *offrampFundingAuthorizationRepository) BeginFinalize(a *models.OfframpFundingAuthorization, revision int, in FinalizeIntent) (bool, error) {
	for name, empty := range map[string]bool{
		"client key hash": in.ClientKeyHash == "", "request hash": in.RequestHash == "",
		"provider key": in.ProviderKey == "", "signed payload": in.SignedPayloadCiphertext == "",
		"payout binding": in.PayoutBinding == "", "lease": in.LeaseUntil.IsZero(), "started at": in.StartedAt.IsZero(),
		"retain until": in.RetainUntil.IsZero(),
	} {
		if empty {
			return false, fmt.Errorf("funding authorization repo: begin finalize: empty %s", name)
		}
	}
	won := false
	err := facades.Orm().Transaction(func(q orm.Query) error {
		tx, err := lockTransaction(q, a.TransactionID)
		if err != nil || tx == nil {
			return err
		}
		if !slices.Contains(unfundedStatuses, tx.Status) {
			return nil
		}
		result, err := q.Model(&models.OfframpFundingAuthorization{}).
			Where("id", a.ID).Where("state", models.FundingAuthPrepared).Where("action_revision", revision).
			Update(map[string]any{
				"state":                       models.FundingAuthFinalizing,
				"finalize_client_key_hash":    in.ClientKeyHash,
				"finalize_request_hash":       in.RequestHash,
				"provider_finalize_key":       in.ProviderKey,
				"finalize_started_at":         in.StartedAt,
				"finalize_lease_expires_at":   in.LeaseUntil,
				"signed_payload_ciphertext":   in.SignedPayloadCiphertext,
				"signed_payload_retain_until": in.RetainUntil,
				"payout_binding":              in.PayoutBinding,
			})
		if err != nil {
			return err
		}
		won = result.RowsAffected > 0
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("funding authorization repo: begin finalize: %w", err)
	}
	if !won {
		return false, nil
	}
	a.State = models.FundingAuthFinalizing
	a.FinalizeClientKeyHash, a.FinalizeRequestHash, a.ProviderFinalizeKey = &in.ClientKeyHash, &in.RequestHash, &in.ProviderKey
	a.FinalizeStartedAt, a.FinalizeLeaseExpiresAt = &in.StartedAt, &in.LeaseUntil
	a.SignedPayloadCiphertext, a.SignedPayloadRetainUntil, a.PayoutBinding = &in.SignedPayloadCiphertext, &in.RetainUntil, &in.PayoutBinding
	return true, nil
}

// The states each kind of finalize result is written from: the automation
// (the send and the recovery) closes what it owns, an operator what was left
// open or held for them.
var (
	automationOwned = []string{models.FundingAuthFinalizing, models.FundingAuthUnknown}
	operatorOwned   = []string{models.FundingAuthUnknown, models.FundingAuthNeedsOperator}
)

func (r *offrampFundingAuthorizationRepository) CompleteFinalized(a *models.OfframpFundingAuthorization, providerOrderID, txHash string) (bool, error) {
	return completeFinalized(a, automationOwned, "complete finalized", providerOrderID, txHash)
}

func (r *offrampFundingAuthorizationRepository) ResolveFinalized(a *models.OfframpFundingAuthorization, providerOrderID, txHash string) (bool, error) {
	return completeFinalized(a, operatorOwned, "resolve finalized", providerOrderID, txHash)
}

func completeFinalized(a *models.OfframpFundingAuthorization, from []string, what, providerOrderID, txHash string) (bool, error) {
	providerOrderID = strings.TrimSpace(providerOrderID)
	if providerOrderID == "" {
		return false, fmt.Errorf("funding authorization repo: %s: empty provider order id", what)
	}
	ok, err := finalizeTransition(a, what,
		func(q orm.Query) (bool, error) {
			return swap(q, a, from,
				map[string]any{"state": models.FundingAuthFinalized, "finalize_lease_expires_at": nil, "recovery_next_attempt_at": nil})
		},
		func(q orm.Query) error {
			set := map[string]any{"status": models.TxStatusProcessing, "gateway_order_id": providerOrderID}
			if h := strings.TrimSpace(txHash); h != "" {
				set["deposit_tx_signature"] = h
			}
			return moveOrder(q, a.TransactionID, set)
		})
	if ok {
		a.State = models.FundingAuthFinalized
	}
	return ok, err
}

func (r *offrampFundingAuthorizationRepository) CompleteFailed(a *models.OfframpFundingAuthorization, code, toStatus string) (bool, error) {
	switch {
	case code == "":
		return false, errors.New("funding authorization repo: complete failed: empty failure code")
	case toStatus != models.TxStatusFailed && toStatus != models.TxStatusExpired:
		return false, fmt.Errorf("funding authorization repo: complete failed: order status %q is not failed or expired", toStatus)
	case toStatus == models.TxStatusExpired && code != models.FundingFailureQuoteExpired:
		return false, fmt.Errorf("funding authorization repo: complete failed: only %s expires an order", models.FundingFailureQuoteExpired)
	}
	return completeFailed(a, automationOwned, "complete failed", code, toStatus)
}

func (r *offrampFundingAuthorizationRepository) ResolveNotCreated(a *models.OfframpFundingAuthorization) (bool, error) {
	return completeFailed(a, operatorOwned, "resolve not created", models.FundingFailurePayoutNotCreated, models.TxStatusFailed)
}

func completeFailed(a *models.OfframpFundingAuthorization, from []string, what, code, toStatus string) (bool, error) {
	ok, err := finalizeTransition(a, what,
		func(q orm.Query) (bool, error) {
			return swap(q, a, from,
				map[string]any{"state": models.FundingAuthFailed, "failure_code": code, "finalize_lease_expires_at": nil, "recovery_next_attempt_at": nil})
		},
		func(q orm.Query) error { return moveOrder(q, a.TransactionID, map[string]any{"status": toStatus}) })
	if ok {
		a.State, a.FailureCode = models.FundingAuthFailed, &code
	}
	return ok, err
}

func (r *offrampFundingAuthorizationRepository) MarkUnknown(a *models.OfframpFundingAuthorization, code string, next time.Time) (bool, error) {
	if code == "" {
		return false, errors.New("funding authorization repo: mark unknown: empty code")
	}
	if next.IsZero() {
		return false, errors.New("funding authorization repo: mark unknown: no next attempt")
	}
	ok, err := finalizeTransition(a, "mark unknown",
		func(q orm.Query) (bool, error) {
			return swap(q, a, automationOwned,
				map[string]any{"state": models.FundingAuthUnknown, "recovery_next_attempt_at": next, "finalize_lease_expires_at": nil})
		},
		func(q orm.Query) error {
			return moveOrder(q, a.TransactionID, map[string]any{"status": models.TxStatusProcessing})
		})
	if ok {
		a.State, a.RecoveryNextAttemptAt, a.FinalizeLeaseExpiresAt = models.FundingAuthUnknown, &next, nil
	}
	return ok, err
}

func (r *offrampFundingAuthorizationRepository) ClaimRecovery(a *models.OfframpFundingAuthorization, now, leaseUntil time.Time) (bool, error) {
	if !leaseUntil.After(now) {
		return false, errors.New("funding authorization repo: claim recovery: lease must end after now")
	}
	claimed := false
	err := facades.Orm().Transaction(func(q orm.Query) error {
		tx, err := lockTransaction(q, a.TransactionID)
		if err != nil || tx == nil {
			return err
		}
		result, err := q.Exec(`UPDATE offramp_funding_authorizations
			SET finalize_lease_expires_at = ?, recovery_attempts = recovery_attempts + 1, updated_at = ?
			WHERE id = ? AND `+dueForRecovery, leaseUntil, time.Now(), a.ID, now, now, now)
		if err != nil {
			return err
		}
		if result.RowsAffected == 0 {
			return nil
		}
		fresh, err := first[models.OfframpFundingAuthorization](q.Where("id", a.ID), "reload")
		if err != nil || fresh == nil {
			return fmt.Errorf("reload claimed authorization %d: %w", a.ID, err)
		}
		a.RecoveryAttempts, a.FinalizeLeaseExpiresAt = fresh.RecoveryAttempts, fresh.FinalizeLeaseExpiresAt
		claimed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("funding authorization repo: claim recovery: %w", err)
	}
	return claimed, nil
}

func (r *offrampFundingAuthorizationRepository) Escalate(a *models.OfframpFundingAuthorization, code string) (bool, error) {
	if code == "" {
		return false, errors.New("funding authorization repo: escalate: empty code")
	}
	ok, err := finalizeTransition(a, "escalate",
		func(q orm.Query) (bool, error) {
			return swap(q, a, automationOwned,
				map[string]any{"state": models.FundingAuthNeedsOperator, "finalize_lease_expires_at": nil})
		}, nil)
	if ok {
		a.State = models.FundingAuthNeedsOperator
	}
	return ok, err
}

func (r *offrampFundingAuthorizationRepository) ListDueForRecovery(now time.Time, limit int) ([]models.OfframpFundingAuthorization, error) {
	var rows []models.OfframpFundingAuthorization
	err := facades.Orm().Query().
		Where(dueForRecovery, now, now, now).
		Where("(? = '' OR transaction_id IN (SELECT t.id FROM transactions t JOIN customers c ON c.id=t.customer_id WHERE c.tenant_key=? AND c.environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR transaction_id IN (SELECT id FROM transactions WHERE customer_id=?))", r.customerID, r.customerID).
		OrderByRaw("CASE WHEN state = 'finalizing' THEN finalize_lease_expires_at ELSE recovery_next_attempt_at END ASC NULLS FIRST, id ASC").
		Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("funding authorization repo: list due for recovery: %w", err)
	}
	return rows, nil
}

func (r *offrampFundingAuthorizationRepository) ClearRetainedPayloads(now time.Time, limit int) (int64, error) {
	result, err := facades.Orm().Query().Exec(`UPDATE offramp_funding_authorizations
		SET signed_payload_ciphertext = NULL, updated_at = ?
		WHERE id IN (
			SELECT id FROM offramp_funding_authorizations
			WHERE state IN ('finalized', 'failed') AND signed_payload_ciphertext IS NOT NULL
				AND signed_payload_retain_until IS NOT NULL AND signed_payload_retain_until <= ?
			ORDER BY id LIMIT ?)`, time.Now(), now, limit)
	if err != nil {
		return 0, fmt.Errorf("funding authorization repo: clear retained payloads: %w", err)
	}
	return result.RowsAffected, nil
}

func (r *offrampFundingAuthorizationRepository) ListNeedingOperator(limit int) ([]FundingAuthorizationWithTransaction, error) {
	var auths []models.OfframpFundingAuthorization
	err := facades.Orm().Query().Where("(? = '' OR transaction_id IN (SELECT t.id FROM transactions t JOIN customers c ON c.id=t.customer_id WHERE c.tenant_key=? AND c.environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR transaction_id IN (SELECT id FROM transactions WHERE customer_id=?))", r.customerID, r.customerID).
		Where("state = ?", models.FundingAuthNeedsOperator).
		Order("updated_at asc").Order("id asc").
		Limit(limit).Find(&auths)
	if err != nil {
		return nil, fmt.Errorf("funding authorization repo: list needing operator: %w", err)
	}
	if len(auths) == 0 {
		return []FundingAuthorizationWithTransaction{}, nil
	}
	ids := make([]uint, 0, len(auths))
	for _, a := range auths {
		ids = append(ids, a.TransactionID)
	}
	var txs []models.Transaction
	if err := facades.Orm().Query().Where("id IN ?", ids).Find(&txs); err != nil {
		return nil, fmt.Errorf("funding authorization repo: list needing operator: transactions: %w", err)
	}
	byID := make(map[uint]models.Transaction, len(txs))
	for _, t := range txs {
		byID[t.ID] = t
	}
	out := make([]FundingAuthorizationWithTransaction, 0, len(auths))
	for _, a := range auths {
		out = append(out, FundingAuthorizationWithTransaction{Authorization: a, Transaction: byID[a.TransactionID]})
	}
	return out, nil
}

// NewOfframpFundingAuthorizationRepositoryForTenant bounds background listings before claiming work.
func NewOfframpFundingAuthorizationRepositoryForTenant(tenant, environment string) OfframpFundingAuthorizationRepository {
	return &offrampFundingAuthorizationRepository{tenant: tenant, environment: environment}
}

// NewOfframpFundingAuthorizationRepositoryForHolder filters attention rows before the per-source limit.
func NewOfframpFundingAuthorizationRepositoryForHolder(tenant, environment string, customerID uint) OfframpFundingAuthorizationRepository {
	return &offrampFundingAuthorizationRepository{tenant: tenant, environment: environment, customerID: customerID}
}
