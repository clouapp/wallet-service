package repositories

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

var (
	ErrActiveKycIntentExists = errors.New("an active kyc intent already exists for this customer and provider")
	ErrStaleKycIntent        = errors.New("kyc intent changed since it was read")
	ErrKycIntentIntegrity    = errors.New("kyc intent material cannot be opened")
	// ErrKycIntentNotFound answers Resolve for an external id no intent has.
	ErrKycIntentNotFound = errors.New("kyc intent not found")
	// ErrKycIntentNotResolvable refuses resolving an intent outside
	// manual_review: anything else is still the automation's to finish.
	ErrKycIntentNotResolvable = errors.New("kyc intent is not in manual review")
	// ErrInvalidKycResolution refuses an outcome outside models.KycIntentOutcomes
	// or a reason longer than its column.
	ErrInvalidKycResolution = errors.New("invalid kyc intent resolution")
)

// Failure codes the recovery claim writes when it quarantines an intent.
const (
	KycIntentCodeAttemptsExhausted = "recovery_attempts_exhausted"
	KycIntentCodeReplayWindowEnded = "safe_replay_window_expired"
	KycIntentCodeIntegrityFailure  = "intent_integrity_failure"
	// KycIntentCodeNeverSent ends an intent that ran out of attempts or of its
	// window without one create ever sent: no customer can exist, so it fails
	// rather than waiting on an operator.
	KycIntentCodeNeverSent = "never_sent"
)

// KycIntentKeyPrefix prefixes the provider idempotency key derived from an
// intent's external id.
const KycIntentKeyPrefix = "kyc-customer:"

const kycIntentActiveUnique = "provider_kyc_submission_intents_active_unique"

// KycIntentMaterial is what an intent carries, encrypted at rest. Images are
// never part of it.
//
// Checkpoint and Prepared are the ramp port's own types: ramp.AtomicKycCheckpoint
// and ramp.PreparedAtomicKyc are aliases of these models types (declared there
// because ramp imports this package), so the material round-trips through the
// port with no conversion.
type KycIntentMaterial struct {
	Version         int                        `json:"version"`
	SubmissionID    string                     `json:"submission_id"`
	ClientIPAddress string                     `json:"client_ip_address"`
	Checkpoint      models.AtomicKycCheckpoint `json:"checkpoint"`
	Prepared        *models.PreparedAtomicKyc  `json:"prepared,omitempty"`
}

type ProviderKycSubmissionIntentRepository interface {
	// Create inserts a preparing intent; the partial unique index answers
	// ErrActiveKycIntentExists for a second active one.
	Create(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error
	// FindActive returns the active intent of (customer, provider) with its
	// material, or (nil, nil, nil). An unreadable material answers
	// ErrKycIntentIntegrity with the intent, so the caller can quarantine it.
	FindActive(customerID uint, provider string) (*models.ProviderKycSubmissionIntent, *KycIntentMaterial, error)
	FindByExternalID(externalID string) (*models.ProviderKycSubmissionIntent, *KycIntentMaterial, error)
	// FindBySubmission returns the intent a submission opened, in any state,
	// or (nil, nil). Its material is not opened: the caller only asks whether
	// the submission already had its one create.
	FindBySubmission(customerID uint, provider, submissionID string) (*models.ProviderKycSubmissionIntent, error)
	Checkpoint(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error
	MarkPrepared(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error
	BeginSubmitting(intent *models.ProviderKycSubmissionIntent, now, leaseUntil time.Time) error
	// RevertAttempt undoes the BeginSubmitting of an attempt that provably
	// never left: state, attempt_count and last_attempt_at go back to
	// before's. The lease stays, for the reschedule that follows.
	RevertAttempt(intent *models.ProviderKycSubmissionIntent, before models.ProviderKycSubmissionIntent) error
	MarkUnknown(intent *models.ProviderKycSubmissionIntent, code string) error
	RecordCustomer(intent *models.ProviderKycSubmissionIntent, customerID string) error
	Complete(intent *models.ProviderKycSubmissionIntent, now time.Time) error
	MarkFailed(intent *models.ProviderKycSubmissionIntent, code string, now time.Time) error
	MarkManualReview(intent *models.ProviderKycSubmissionIntent, code string, retainUntil time.Time) error
	// ScheduleRecovery sets the next attempt and clears any lease.
	ScheduleRecovery(intent *models.ProviderKycSubmissionIntent, at time.Time, code string) error
	// ClaimRecoveryDue locks up to limit intents in preparing/prepared/
	// submitting/unknown whose next attempt or lease is due (FOR UPDATE SKIP
	// LOCKED), moves those past maxAttempts or their retry deadline to
	// manual_review — or, when not one create was ever sent, to failed, and
	// returns those Abandoned — and leases the rest until leaseUntil,
	// returning them.
	ClaimRecoveryDue(now, leaseUntil time.Time, limit, maxAttempts int, retainUntil time.Time) ([]ClaimedKycIntent, error)
	Resolve(externalID, outcome, reason string, now time.Time) error
	// ListManualReview returns up to limit intents in manual_review, oldest
	// updated_at first. Their material is not opened.
	ListManualReview(limit int) ([]models.ProviderKycSubmissionIntent, error)
	// ScrubExpiredMaterial clears the material of terminal or resolved
	// intents whose retention passed, and of completed/failed ones at once.
	ScrubExpiredMaterial(now time.Time, limit int) (int, error)
}

type ClaimedKycIntent struct {
	Intent   models.ProviderKycSubmissionIntent
	Material *KycIntentMaterial // nil when it cannot be opened (quarantined), or Abandoned
	// Abandoned is an intent the claim failed (KycIntentCodeNeverSent): out of
	// attempts or of its window with nothing ever sent. Nothing is dispatched
	// from it; its row is the caller's to let go.
	Abandoned bool
}

type providerKycSubmissionIntentRepository struct {
	tenant, environment string
	customerID          uint
}

func NewProviderKycSubmissionIntentRepository() ProviderKycSubmissionIntentRepository {
	return &providerKycSubmissionIntentRepository{}
}

// Every time is written in UTC: the columns are timestamps without a zone,
// and the claim compares them against the caller's clock in SQL, so a value
// stored in another location would be due at the wrong instant.
func utc(t time.Time) time.Time { return t.UTC() }

func utcPtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

func sealKycIntentMaterial(material KycIntentMaterial) (string, error) {
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("kyc intent repo: encode material: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(encoded))
	if err != nil {
		return "", fmt.Errorf("kyc intent repo: seal material: %w", err)
	}
	return sealed, nil
}

// openKycIntentMaterial opens a sealed material. Any failure — a tampered
// ciphertext, a rotated key, JSON that no longer decodes — is the same
// ErrKycIntentIntegrity: none of them can be replayed safely.
func openKycIntentMaterial(sealed string) (*KycIntentMaterial, error) {
	plaintext, err := facades.Crypt().DecryptString(sealed)
	if err != nil {
		return nil, ErrKycIntentIntegrity
	}
	var material KycIntentMaterial
	if err := json.Unmarshal([]byte(plaintext), &material); err != nil {
		return nil, ErrKycIntentIntegrity
	}
	return &material, nil
}

func (r *providerKycSubmissionIntentRepository) Create(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error {
	if intent.RetryDeadline.IsZero() {
		return errors.New("kyc intent repo: create: retry deadline is required")
	}
	if intent.ExternalID == "" {
		intent.ExternalID = uuid.NewString()
	}
	if intent.ProviderIdempotencyKey == "" {
		intent.ProviderIdempotencyKey = KycIntentKeyPrefix + intent.ExternalID
	}
	sealed, err := sealKycIntentMaterial(material)
	if err != nil {
		return err
	}
	intent.State = models.KycIntentPreparing
	intent.MaterialCiphertext = sealed
	intent.BodySHA256 = nil
	intent.Revision = 1
	intent.RetryDeadline = utc(intent.RetryDeadline)
	if err := facades.Orm().Query().Create(intent); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == kycIntentActiveUnique {
			return ErrActiveKycIntentExists
		}
		return fmt.Errorf("kyc intent repo: create: %w", err)
	}
	return nil
}

func (r *providerKycSubmissionIntentRepository) FindActive(customerID uint, provider string) (*models.ProviderKycSubmissionIntent, *KycIntentMaterial, error) {
	intent, err := first[models.ProviderKycSubmissionIntent](facades.Orm().Query().
		Where("customer_id", customerID).Where("provider", provider).
		WhereIn("state", anySlice(models.ActiveKycIntentStates)), "kyc intent repo: find active")
	return withMaterial(intent, err)
}

func (r *providerKycSubmissionIntentRepository) FindByExternalID(externalID string) (*models.ProviderKycSubmissionIntent, *KycIntentMaterial, error) {
	// Postgres refuses a malformed uuid with an error; to the caller it is
	// simply an id no intent has.
	if _, err := uuid.Parse(externalID); err != nil {
		return nil, nil, nil
	}
	intent, err := first[models.ProviderKycSubmissionIntent](facades.Orm().Query().
		Where("external_id", externalID), "kyc intent repo: find by external id")
	return withMaterial(intent, err)
}

// FindBySubmission reads through provider_kyc_submission_intents_submission_index
// (customer_id, provider, submission_id).
func (r *providerKycSubmissionIntentRepository) FindBySubmission(
	customerID uint, provider, submissionID string,
) (*models.ProviderKycSubmissionIntent, error) {
	return first[models.ProviderKycSubmissionIntent](facades.Orm().Query().
		Where("customer_id", customerID).Where("provider", provider).Where("submission_id", submissionID).
		Order("id"), "kyc intent repo: find by submission")
}

func withMaterial(intent *models.ProviderKycSubmissionIntent, err error) (*models.ProviderKycSubmissionIntent, *KycIntentMaterial, error) {
	if err != nil || intent == nil {
		return nil, nil, err
	}
	material, err := openKycIntentMaterial(intent.MaterialCiphertext)
	if err != nil {
		return intent, nil, err
	}
	return intent, material, nil
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func (r *providerKycSubmissionIntentRepository) Checkpoint(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error {
	sealed, err := sealKycIntentMaterial(material)
	if err != nil {
		return err
	}
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.MaterialCiphertext = sealed
	}, "material_ciphertext")
}

// MarkPrepared stores the material with its prepared body and the body's
// digest. A material without one leaves body_sha256 null, which the table's
// CHECK refuses for a prepared intent.
func (r *providerKycSubmissionIntentRepository) MarkPrepared(intent *models.ProviderKycSubmissionIntent, material KycIntentMaterial) error {
	sealed, err := sealKycIntentMaterial(material)
	if err != nil {
		return err
	}
	var digest *string
	if material.Prepared != nil && material.Prepared.PayloadSHA256 != "" {
		digest = &material.Prepared.PayloadSHA256
	}
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentPrepared
		next.MaterialCiphertext = sealed
		next.BodySHA256 = digest
	}, "state", "material_ciphertext", "body_sha256")
}

// BeginSubmitting counts an attempt and leases the intent for its length, so
// a worker that dies mid-create leaves it to the recovery sweep.
func (r *providerKycSubmissionIntentRepository) BeginSubmitting(intent *models.ProviderKycSubmissionIntent, now, leaseUntil time.Time) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentSubmitting
		next.AttemptCount++
		next.LastAttemptAt = utcPtr(now)
		next.RecoveryLeaseExpiresAt = utcPtr(leaseUntil)
		next.RecoveryNextAttemptAt = nil
	}, "state", "attempt_count", "last_attempt_at", "recovery_lease_expires_at", "recovery_next_attempt_at")
}

// RevertAttempt is BeginSubmitting undone. It leaves the lease where
// BeginSubmitting put it, so an intent nothing reschedules is still handed to
// the sweep when it expires, and never touches recovery_attempts: only the
// claim counts those.
func (r *providerKycSubmissionIntentRepository) RevertAttempt(
	intent *models.ProviderKycSubmissionIntent, before models.ProviderKycSubmissionIntent,
) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = before.State
		next.AttemptCount = before.AttemptCount
		next.LastAttemptAt = before.LastAttemptAt
	}, "state", "attempt_count", "last_attempt_at")
}

// MarkUnknown keeps the lease: if nothing reschedules the intent, its expiry
// is what hands it to the sweep.
func (r *providerKycSubmissionIntentRepository) MarkUnknown(intent *models.ProviderKycSubmissionIntent, code string) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentUnknown
		next.FailureCode = &code
	}, "state", "failure_code")
}

func (r *providerKycSubmissionIntentRepository) RecordCustomer(intent *models.ProviderKycSubmissionIntent, customerID string) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.ProviderCustomerID = &customerID
	}, "provider_customer_id")
}

// Complete and MarkFailed end the intent. Their material is due for the scrub
// at once: nothing will replay it.
func (r *providerKycSubmissionIntentRepository) Complete(intent *models.ProviderKycSubmissionIntent, now time.Time) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentCompleted
		next.MaterialRetainUntil = utcPtr(now)
		next.RecoveryNextAttemptAt, next.RecoveryLeaseExpiresAt = nil, nil
	}, "state", "material_retain_until", "recovery_next_attempt_at", "recovery_lease_expires_at")
}

func (r *providerKycSubmissionIntentRepository) MarkFailed(intent *models.ProviderKycSubmissionIntent, code string, now time.Time) error {
	return markFailed(facades.Orm().Query(), intent, code, now)
}

func markFailed(q orm.Query, intent *models.ProviderKycSubmissionIntent, code string, now time.Time) error {
	return writeKycIntent(q, intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentFailed
		next.FailureCode = &code
		next.MaterialRetainUntil = utcPtr(now)
		next.RecoveryNextAttemptAt, next.RecoveryLeaseExpiresAt = nil, nil
	}, "state", "failure_code", "material_retain_until", "recovery_next_attempt_at", "recovery_lease_expires_at")
}

func (r *providerKycSubmissionIntentRepository) MarkManualReview(intent *models.ProviderKycSubmissionIntent, code string, retainUntil time.Time) error {
	return markManualReview(facades.Orm().Query(), intent, code, retainUntil)
}

// markManualReview takes the intent out of the automation's hands. The
// material is kept until retainUntil, for the operator who decides it.
func markManualReview(q orm.Query, intent *models.ProviderKycSubmissionIntent, code string, retainUntil time.Time) error {
	return writeKycIntent(q, intent, func(next *models.ProviderKycSubmissionIntent) {
		next.State = models.KycIntentManualReview
		next.FailureCode = &code
		next.MaterialRetainUntil = utcPtr(retainUntil)
		next.RecoveryNextAttemptAt, next.RecoveryLeaseExpiresAt = nil, nil
	}, "state", "failure_code", "material_retain_until", "recovery_next_attempt_at", "recovery_lease_expires_at")
}

// ScheduleRecovery only sets when the next attempt is due and drops any
// lease. It does not count a recovery attempt: the sweeper's claim is the only
// thing that does (see ClaimRecoveryDue).
func (r *providerKycSubmissionIntentRepository) ScheduleRecovery(intent *models.ProviderKycSubmissionIntent, at time.Time, code string) error {
	return writeKycIntent(facades.Orm().Query(), intent, func(next *models.ProviderKycSubmissionIntent) {
		next.RecoveryNextAttemptAt = utcPtr(at)
		next.RecoveryLeaseExpiresAt = nil
		if code != "" {
			next.FailureCode = &code
		}
	}, "recovery_next_attempt_at", "recovery_lease_expires_at", "failure_code")
}

// ClaimRecoveryDue selects with FOR UPDATE SKIP LOCKED, which the query builder
// cannot express, so that read is the one raw statement here.
//
// It runs in one transaction, so the rows it locked stay locked
// until each is leased or quarantined: a second sweep skips them rather than
// waiting, and never claims what this one is about to lease.
//
// The claim is the only thing that counts a recovery attempt, and the count is
// what maxAttempts is checked against. Counting here means a worker that dies
// every time it is dispatched still reaches manual review, instead of being
// leased again on every expiry until the replay window runs out.
func (r *providerKycSubmissionIntentRepository) ClaimRecoveryDue(
	now, leaseUntil time.Time, limit, maxAttempts int, retainUntil time.Time,
) ([]ClaimedKycIntent, error) {
	if limit <= 0 {
		return nil, nil
	}
	now = utc(now)
	var claimed []ClaimedKycIntent
	err := facades.Orm().Transaction(func(tx orm.Query) error {
		var due []struct{ ID uint }
		if err := tx.Raw(`SELECT id FROM provider_kyc_submission_intents
			WHERE state IN ?
 AND (? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))
			  AND ((recovery_next_attempt_at IS NOT NULL AND recovery_next_attempt_at <= ?)
			    OR (recovery_lease_expires_at IS NOT NULL AND recovery_lease_expires_at <= ?))
			ORDER BY COALESCE(recovery_next_attempt_at, recovery_lease_expires_at), id
			LIMIT ?
			FOR UPDATE SKIP LOCKED`,
			models.RecoverableKycIntentStates, r.tenant, r.tenant, r.environment, now, now, limit).Scan(&due); err != nil {
			return fmt.Errorf("kyc intent repo: lock due intents: %w", err)
		}
		if len(due) == 0 {
			return nil
		}
		ids := make([]any, len(due))
		for i, row := range due {
			ids[i] = row.ID
		}
		var intents []models.ProviderKycSubmissionIntent
		if err := tx.WhereIn("id", ids).Order("id").Find(&intents); err != nil {
			return fmt.Errorf("kyc intent repo: load due intents: %w", err)
		}
		for i := range intents {
			intent := &intents[i]
			exhausted, expired := intent.RecoveryAttempts >= maxAttempts, !intent.RetryDeadline.After(now)
			switch {
			case (exhausted || expired) && intent.AttemptCount == 0:
				// Not one create left: no customer can exist, and an
				// operator would have nothing to decide.
				if err := markFailed(tx, intent, KycIntentCodeNeverSent, now); err != nil {
					return err
				}
				claimed = append(claimed, ClaimedKycIntent{Intent: *intent, Abandoned: true})
				continue
			case exhausted:
				if err := markManualReview(tx, intent, KycIntentCodeAttemptsExhausted, retainUntil); err != nil {
					return err
				}
				continue
			case expired:
				if err := markManualReview(tx, intent, KycIntentCodeReplayWindowEnded, retainUntil); err != nil {
					return err
				}
				continue
			}
			material, err := openKycIntentMaterial(intent.MaterialCiphertext)
			if err != nil {
				if err := markManualReview(tx, intent, KycIntentCodeIntegrityFailure, retainUntil); err != nil {
					return err
				}
				claimed = append(claimed, ClaimedKycIntent{Intent: *intent})
				continue
			}
			if err := writeKycIntent(tx, intent, func(next *models.ProviderKycSubmissionIntent) {
				next.RecoveryLeaseExpiresAt = utcPtr(leaseUntil)
				next.RecoveryNextAttemptAt = nil
				next.RecoveryAttempts++
			}, "recovery_lease_expires_at", "recovery_next_attempt_at", "recovery_attempts"); err != nil {
				return err
			}
			claimed = append(claimed, ClaimedKycIntent{Intent: *intent, Material: material})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// KycIntentReasonMaxLength is the most characters a resolution reason holds:
// its column's length, which Postgres counts in characters, not bytes.
const KycIntentReasonMaxLength = 255

// Resolve closes an intent in manual review with an operator's verdict.
func (r *providerKycSubmissionIntentRepository) Resolve(externalID, outcome, reason string, now time.Time) error {
	if !ValidKycResolution(outcome, reason) {
		return ErrInvalidKycResolution
	}
	if _, err := uuid.Parse(externalID); err != nil {
		return ErrKycIntentNotFound
	}
	return facades.Orm().Transaction(func(tx orm.Query) error {
		intent, err := first[models.ProviderKycSubmissionIntent](tx.LockForUpdate().
			Where("external_id", externalID), "kyc intent repo: resolve")
		if err != nil {
			return err
		}
		if intent == nil {
			return ErrKycIntentNotFound
		}
		if intent.State != models.KycIntentManualReview {
			return ErrKycIntentNotResolvable
		}
		return writeKycIntent(tx, intent, func(next *models.ProviderKycSubmissionIntent) {
			next.State = models.KycIntentResolved
			next.ResolvedAt = utcPtr(now)
			next.ResolutionOutcome = &outcome
			next.ResolutionReason = &reason
		}, "state", "resolved_at", "resolution_outcome", "resolution_reason")
	})
}

// ValidKycResolution reports whether outcome is one of models.KycIntentOutcomes
// and reason fits its column.
func ValidKycResolution(outcome, reason string) bool {
	return containsString(models.KycIntentOutcomes, outcome) &&
		utf8.RuneCountInString(reason) <= KycIntentReasonMaxLength
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// ScrubExpiredMaterial is one raw UPDATE over a FOR UPDATE SKIP LOCKED subselect
// with a LIMIT: a concurrent scrub skips the rows another holds instead of
// waiting on them, which the query builder cannot express.
//
// It replaces the material with a sealed empty one rather
// than an empty string: the column stays something every reader opens, so a
// scrubbed intent never reads as a tampered one.
func (r *providerKycSubmissionIntentRepository) ScrubExpiredMaterial(now time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	stub, err := sealKycIntentMaterial(KycIntentMaterial{})
	if err != nil {
		return 0, err
	}
	now = utc(now)
	res, err := facades.Orm().Query().Exec(`UPDATE provider_kyc_submission_intents
		SET material_ciphertext = ?, material_cleared_at = ?, updated_at = ?, revision = revision + 1
		WHERE id IN (
			SELECT id FROM provider_kyc_submission_intents
			WHERE material_cleared_at IS NULL
			  AND (state IN ?
			    OR (state IN ? AND material_retain_until IS NOT NULL AND material_retain_until <= ?))
			ORDER BY id
			LIMIT ?
			FOR UPDATE SKIP LOCKED)`,
		stub, now, now,
		[]string{models.KycIntentCompleted, models.KycIntentFailed},
		[]string{models.KycIntentManualReview, models.KycIntentResolved}, now,
		limit)
	if err != nil {
		return 0, fmt.Errorf("kyc intent repo: scrub material: %w", err)
	}
	return int(res.RowsAffected), nil
}

// writeKycIntent applies change to a copy of intent and writes the named
// columns under the revision guard. Only a write that matched the revision it
// read lands; intent takes the new values (and revision) only then, so a
// refused write leaves the caller's copy as the row it last saw.
func writeKycIntent(q orm.Query, intent *models.ProviderKycSubmissionIntent,
	change func(next *models.ProviderKycSubmissionIntent), columns ...string,
) error {
	next := *intent
	change(&next)
	now := time.Now().UTC()

	updates := make(map[string]any, len(columns)+2)
	for _, column := range columns {
		updates[column] = kycIntentColumn(&next, column)
	}
	updates["updated_at"] = now
	updates["revision"] = intent.Revision + 1

	// Keyed by id on a fresh model, not on intent: the revision in the WHERE is
	// what makes the write lose to a newer one, and the model's own fields must
	// not be what GORM writes.
	res, err := q.Model(&models.ProviderKycSubmissionIntent{}).
		Where("id", intent.ID).Where("revision", intent.Revision).
		Update(updates)
	if err != nil {
		return fmt.Errorf("kyc intent repo: update: %w", err)
	}
	if res.RowsAffected != 1 {
		return ErrStaleKycIntent
	}
	next.Revision = intent.Revision + 1
	*intent = next
	return nil
}

// kycIntentColumn is the value of one mutable column. A column missing here is
// a programming error, and panics in every test that touches it.
func kycIntentColumn(intent *models.ProviderKycSubmissionIntent, column string) any {
	switch column {
	case "state":
		return intent.State
	case "material_ciphertext":
		return intent.MaterialCiphertext
	case "body_sha256":
		return intent.BodySHA256
	case "provider_customer_id":
		return intent.ProviderCustomerID
	case "attempt_count":
		return intent.AttemptCount
	case "last_attempt_at":
		return intent.LastAttemptAt
	case "recovery_next_attempt_at":
		return intent.RecoveryNextAttemptAt
	case "recovery_lease_expires_at":
		return intent.RecoveryLeaseExpiresAt
	case "recovery_attempts":
		return intent.RecoveryAttempts
	case "failure_code":
		return intent.FailureCode
	case "material_retain_until":
		return intent.MaterialRetainUntil
	case "resolved_at":
		return intent.ResolvedAt
	case "resolution_outcome":
		return intent.ResolutionOutcome
	case "resolution_reason":
		return intent.ResolutionReason
	}
	panic("kyc intent repo: unknown column " + column)
}

func (r *providerKycSubmissionIntentRepository) ListManualReview(limit int) ([]models.ProviderKycSubmissionIntent, error) {
	var rows []models.ProviderKycSubmissionIntent
	err := facades.Orm().Query().Where("(? = '' OR customer_id IN (SELECT id FROM customers WHERE tenant_key=? AND environment=?))", r.tenant, r.tenant, r.environment).Where("(? = 0 OR customer_id = ?)", r.customerID, r.customerID).
		Where("state = ?", models.KycIntentManualReview).
		Order("updated_at asc").Order("id asc").
		Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("kyc intent repo: list manual review: %w", err)
	}
	return rows, nil
}

// NewProviderKycSubmissionIntentRepositoryForTenant bounds background listings before claiming work.
func NewProviderKycSubmissionIntentRepositoryForTenant(tenant, environment string) ProviderKycSubmissionIntentRepository {
	return &providerKycSubmissionIntentRepository{tenant: tenant, environment: environment}
}

// NewProviderKycSubmissionIntentRepositoryForHolder filters attention rows before the per-source limit.
func NewProviderKycSubmissionIntentRepositoryForHolder(tenant, environment string, customerID uint) ProviderKycSubmissionIntentRepository {
	return &providerKycSubmissionIntentRepository{tenant: tenant, environment: environment, customerID: customerID}
}
