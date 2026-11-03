package kyc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// ErrStagingExpired means the staged payload is gone: the worker took longer
// than StagingTTL, or the job ran twice and the first run consumed it.
var ErrStagingExpired = errors.New("kyc: staged submission expired")

// StagingTTL bounds how long identity documents may sit outside the request
// that carried them. It is long enough to absorb a queue backlog and short
// enough that a stalled worker does not leave photographs of someone's ID in
// Redis for the afternoon.
const StagingTTL = 15 * time.Minute

const stagingKeyPrefix = "kyc:staging:"

// The in-flight mark answers "which kind of submission is already queued for
// this customer at this provider". The row cannot: it records that something is in
// flight, as `submitting`, but not what — and documents are allowed to chain
// behind an application while a second documents upload is a duplicate.
//
// The owner key is the reverse index. Forget is called at every terminal path
// and knows only the submission id, so it is what lets the mark die with the
// payload instead of waiting out the TTL.
//
// The mark names the submission that wrote it (`kind:id`), because the latest
// one staged replaces it: documents queued behind an application are what the
// mark says once they are staged, and the application finishing first must not
// clear it. Cleared, the documents in flight looked like nothing at all — the
// window where the provider has answered the application, asking for the very
// files still in the queue.
const (
	inFlightKeyPrefix      = "kyc:inflight:"
	inFlightOwnerKeyPrefix = "kyc:inflight-of:"
)

func inFlightKey(customerID uint, provider string) string {
	return fmt.Sprintf("%s%d:%s", inFlightKeyPrefix, customerID, provider)
}

// StagedSubmission is what a worker needs to finish a submission the request
// only accepted.
//
// It exists because document submission cannot happen inside the request: the
// HTTP layer cancels at three seconds while a Bridge customer create with
// images takes longer, and the handler goroutine would finish with nobody left
// to persist the result. The request stages this, the worker sends it.
type StagedSubmission struct {
	// CustomerID is whose row, link and in-flight mark it is; UserID is who
	// sent it, for the provider timeline. A payload staged before
	// CustomerID existed carries the user's id alone, which is its
	// individual customer's; the worker reads the customer from its job.
	CustomerID uint   `json:"customer_id"`
	UserID     uint   `json:"user_id"`
	Provider   string `json:"provider"`
	// Kind selects which adapter call the worker makes.
	Kind string `json:"kind"`
	// PreviousStatus is the compliance status the row held before it was marked
	// in flight. A submission that never reaches the provider changes nothing
	// upstream, so this is what the worker restores — claiming a review that is
	// not happening would be worse than saying nothing changed.
	PreviousStatus string `json:"previous_status"`
	// StagedAt is when Put sealed the payload, so the worker can tell how much
	// of StagingTTL is left before it re-queues a transient failure. Zero on a
	// payload staged before the field existed, which reads as out of time.
	StagedAt time.Time `json:"staged_at"`
	// Application is set for KindApplication, Documents for KindDocuments.
	// Both carry base64 images and are PII from end to end: they are never
	// logged, and they live only inside the sealed blob below.
	Application *ramp.Application `json:"application,omitempty"`
	Documents   *ramp.Documents   `json:"documents,omitempty"`
	// AtomicCheckpoint is what an atomic create's prepare has uploaded so far
	// (KindAtomicCustomer only). The intent holds the durable copy; this one
	// travels with the documents it was uploaded from.
	AtomicCheckpoint *ramp.AtomicKycCheckpoint `json:"atomic_checkpoint,omitempty"`
	// DispatchAttempts counts how often the worker re-queued this submission
	// itself (KindAtomicCustomer only). The intent's recovery_attempts counts
	// only the sweeper's claims, so without this the worker's own backoff
	// would never grow.
	DispatchAttempts int `json:"dispatch_attempts,omitempty"`
	// resumed marks a submission rebuilt from its intent after the staging
	// expired: there is no blob to update, so it never re-queues itself.
	resumed bool
}

// The submission kinds. KindAtomicCustomer is the documents of a provider
// whose customer is born whole (ramp.AtomicKycCreator): they create the
// customer, from the application deferred until they arrived.
const (
	KindApplication    = "application"
	KindDocuments      = "documents"
	KindAtomicCustomer = "atomic_customer"
)

// Staging holds a submission between the request that accepted it and the
// worker that sends it. The payload is encrypted at rest: Redis is a cache
// with a dump file, a MONITOR command and an operator, and none of those
// should ever see a customer's identity documents.
type Staging interface {
	// Put seals the submission and returns the id the queue argument carries.
	Put(submission StagedSubmission) (string, error)
	// Get opens a staged submission. It does not consume it — a job that fails
	// before persisting must be able to read the same payload again.
	Get(id string) (*StagedSubmission, error)
	// Update seals submission again under id without extending its TTL: the
	// payload still expires StagingTTL after it was first staged. A payload
	// already gone is ErrStagingExpired.
	Update(id string, submission StagedSubmission) error
	// Forget removes the payload and the in-flight mark that came with it.
	// Called as soon as the outcome is known, so the TTL is a backstop rather
	// than the normal lifetime.
	Forget(id string)
	// InFlightKind reports the kind of submission already staged for this
	// customer at this provider, or "" when none is — including when the mark
	// was lost.
	// Callers must fail open on "": refusing on an absent mark would put the
	// client back to polling for a customer before daring to send documents.
	InFlightKind(customerID uint, provider string) string
}

// CacheStaging is the Redis-backed Staging.
type CacheStaging struct{}

// NewCacheStaging builds the staging store.
func NewCacheStaging() *CacheStaging { return &CacheStaging{} }

// Put seals the submission under a fresh random id.
//
// The id doubles as the submission's idempotency seed: it is generated once,
// travels in the queue argument, and is therefore stable across every attempt
// at the same submission while differing from the next one. That is exactly
// the property an idempotency key needs — a retry must not create a second
// customer, and a genuine resubmission after a rejection must not be swallowed
// as a duplicate of the first.
func (s *CacheStaging) Put(submission StagedSubmission) (string, error) {
	id, err := newStagingID()
	if err != nil {
		return "", err
	}
	submission.StagedAt = time.Now()
	encoded, err := json.Marshal(submission)
	if err != nil {
		return "", fmt.Errorf("kyc staging: encode: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(encoded))
	if err != nil {
		return "", fmt.Errorf("kyc staging: encrypt: %w", err)
	}
	if err := facades.Cache().Put(stagingKeyPrefix+id, sealed, StagingTTL); err != nil {
		return "", fmt.Errorf("kyc staging: store: %w", err)
	}
	// The mark is best-effort: losing it costs the duplicate check, which fails
	// open, while failing the submission over it would cost the submission.
	owner := inFlightKey(submission.CustomerID, submission.Provider)
	if err := facades.Cache().Put(owner, submission.Kind+":"+id, StagingTTL); err != nil {
		facades.Log().Errorf("kyc staging: mark in flight: %v", err)
	} else if err := facades.Cache().Put(
		inFlightOwnerKeyPrefix+id, owner, StagingTTL); err != nil {
		facades.Log().Errorf("kyc staging: index in-flight mark: %v", err)
	}
	return id, nil
}

// Get opens a staged submission, or reports that it is gone.
func (s *CacheStaging) Get(id string) (*StagedSubmission, error) {
	sealed := facades.Cache().GetString(stagingKeyPrefix + id)
	if sealed == "" {
		return nil, ErrStagingExpired
	}
	plaintext, err := facades.Crypt().DecryptString(sealed)
	if err != nil {
		return nil, fmt.Errorf("kyc staging: decrypt: %w", err)
	}
	var submission StagedSubmission
	if err := json.Unmarshal([]byte(plaintext), &submission); err != nil {
		return nil, fmt.Errorf("kyc staging: decode: %w", err)
	}
	return &submission, nil
}

// Update reseals a staged submission for what is left of its TTL, read from
// StagedAt, so a worker writing its progress never keeps the images in Redis
// past the deadline the first Put set.
//
// A payload already gone is refused rather than written back. The check and
// the write are two cache calls — the cache contract has no write-if-exists
// (Redis SET XX) — so a Forget landing between them is undone by the write:
// the payload is stored again, for at most what was left of its TTL. Only the
// worker that holds the submission calls both, so the window is its own
// pass's, and the TTL stays the bound.
func (s *CacheStaging) Update(id string, submission StagedSubmission) error {
	if facades.Cache().GetString(stagingKeyPrefix+id) == "" {
		return ErrStagingExpired
	}
	remaining := StagingTTL - time.Since(submission.StagedAt)
	if remaining <= 0 {
		return ErrStagingExpired
	}
	encoded, err := json.Marshal(submission)
	if err != nil {
		return fmt.Errorf("kyc staging: encode: %w", err)
	}
	sealed, err := facades.Crypt().EncryptString(string(encoded))
	if err != nil {
		return fmt.Errorf("kyc staging: encrypt: %w", err)
	}
	if err := facades.Cache().Put(stagingKeyPrefix+id, sealed, remaining); err != nil {
		return fmt.Errorf("kyc staging: store: %w", err)
	}
	return nil
}

// Forget removes the payload and releases the in-flight mark it was staged
// with, so the next submission is not refused for the rest of the TTL.
func (s *CacheStaging) Forget(id string) {
	facades.Cache().Forget(stagingKeyPrefix + id)
	owner := facades.Cache().GetString(inFlightOwnerKeyPrefix + id)
	if owner == "" {
		return
	}
	facades.Cache().Forget(inFlightOwnerKeyPrefix + id)
	// Only this submission's own mark: a later one staged for the same
	// customer and provider has replaced it and is still in flight. A mark naming no
	// submission predates the id and is released as it always was.
	if _, markID, named := strings.Cut(facades.Cache().GetString(owner), ":"); !named || markID == id {
		facades.Cache().Forget(owner)
	}
}

// InFlightKind reads the mark. An absent mark reads as "nothing in flight":
// the caller fails open, and the row status is still the outer guard.
func (s *CacheStaging) InFlightKind(customerID uint, provider string) string {
	kind, _, _ := strings.Cut(facades.Cache().GetString(inFlightKey(customerID, provider)), ":")
	return kind
}

// newStagingID returns 128 bits of randomness as hex. It is an unguessable
// handle to somebody's identity documents, so it comes from crypto/rand and
// nothing derived from the user.
func newStagingID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("kyc staging: generate id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

var _ Staging = (*CacheStaging)(nil)
