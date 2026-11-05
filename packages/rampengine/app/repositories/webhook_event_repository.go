package repositories

import (
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// WebhookEventRepository is the persistence boundary for gateway webhook events.
type WebhookEventRepository interface {
	// CreateIfNew inserts the event, returning (inserted=true) if it was new and
	// (inserted=false) if (provider, event_id) already exists. The idempotency key.
	CreateIfNew(event *models.WebhookEvent) (bool, error)
	FindByID(id uint) (*models.WebhookEvent, error)
	// WithLocked opens a transaction, takes SELECT … FOR UPDATE on the event as
	// its first statement and runs fn with the locked row (nil when there is
	// none) and the transaction. The lock is held until the transaction commits
	// or rolls back, after fn returns: a second caller for the same event waits
	// on it and then reads what the first one wrote. fn's error rolls back.
	WithLocked(id uint, fn func(locked *models.WebhookEvent, tx WebhookTx) error) error
	// ListByTransactionID returns every event stamped with that transaction,
	// oldest received_at first — the operator timeline. Ties break on id so two
	// deliveries in the same second stay stable. The transaction-detail screen
	// (screen 6) and the webhook explorer both use this.
	ListByTransactionID(transactionID uint) ([]models.WebhookEvent, error)
	// AdminPaginate is the cross-event explorer. Empty/zero/nil filters are ignored.
	AdminPaginate(filters AdminWebhookEventListFilters) ([]models.WebhookEvent, int64, error)
	// FindUnprocessed returns up to limit events with no ProcessedAt received
	// before olderThan — events whose queue dispatch was lost or whose retries
	// were exhausted. Ordered oldest-first so a backlog drains in arrival order.
	FindUnprocessed(olderThan time.Time, limit int) ([]models.WebhookEvent, error)
	// LastReceivedAt is the received_at of the provider's most recent event, nil
	// when it has never delivered one.
	LastReceivedAt(provider string) (*time.Time, error)
}

// Processed filter values for the admin explorer. They map onto processed_at
// and processing_error rather than a dedicated column.
const (
	AdminWebhookProcessedYes   = "yes"
	AdminWebhookProcessedNo    = "no"
	AdminWebhookProcessedError = "error"
)

// AdminWebhookEventListFilters scopes the admin-facing webhook event listing.
// Empty string / zero / nil fields are ignored by AdminPaginate.
type AdminWebhookEventListFilters struct {
	Provider      string
	EventType     string
	Processed     string // yes | no | error
	TransactionID uint
	ExternalRef   string
	From          *time.Time // inclusive lower bound on received_at
	To            *time.Time // exclusive upper bound on received_at
	Page          int
	PerPage       int
	Sort          string // received_at|-received_at|processed_at|-processed_at
}

type webhookEventRepository struct{}

// NewWebhookEventRepository returns a WebhookEventRepository backed by the
// default ORM connection.
func NewWebhookEventRepository() WebhookEventRepository {
	return &webhookEventRepository{}
}

func (r *webhookEventRepository) CreateIfNew(event *models.WebhookEvent) (bool, error) {
	// Check first; rely on the unique index for the race window.
	existing, err := first[models.WebhookEvent](facades.Orm().Query().
		Where("provider", event.Provider).
		Where("event_id", event.EventID), "webhook event repo: lookup")
	if err != nil || existing != nil {
		return false, err
	}
	if err := facades.Orm().Query().Create(event); err != nil {
		// Treat unique-violation on (provider, event_id) as a benign duplicate.
		if isUniqueViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("webhook event repo: create: %w", err)
	}
	return true, nil
}

func (r *webhookEventRepository) FindByID(id uint) (*models.WebhookEvent, error) {
	return first[models.WebhookEvent](facades.Orm().Query().Where("id", id), "webhook event repo: find by id")
}

func (r *webhookEventRepository) FindByIDForUpdate(q orm.Query, id uint) (*models.WebhookEvent, error) {
	return first[models.WebhookEvent](q.LockForUpdate().Where("id", id), "webhook event repo: find by id for update")
}

func (r *webhookEventRepository) WithLocked(id uint, fn func(*models.WebhookEvent, WebhookTx) error) error {
	return facades.Orm().Transaction(func(q orm.Query) error {
		locked, err := r.FindByIDForUpdate(q, id)
		if err != nil {
			return &LockError{Err: err}
		}
		return fn(locked, webhookTx{q})
	})
}

// LockError is WithLocked failing to take the lock, as opposed to fn failing.
// It reads as the error it wraps, so a caller adds its own context only.
type LockError struct{ Err error }

func (e *LockError) Error() string { return e.Err.Error() }
func (e *LockError) Unwrap() error { return e.Err }

func (r *webhookEventRepository) ListByTransactionID(transactionID uint) ([]models.WebhookEvent, error) {
	var events []models.WebhookEvent
	err := facades.Orm().Query().
		Where("transaction_id", transactionID).
		OrderBy("received_at", "asc").
		OrderBy("id", "asc").
		Find(&events)
	if err != nil {
		return nil, fmt.Errorf("webhook event repo: list by transaction: %w", err)
	}
	return events, nil
}

func (r *webhookEventRepository) AdminPaginate(filters AdminWebhookEventListFilters) ([]models.WebhookEvent, int64, error) {
	q := facades.Orm().Query().Model(&models.WebhookEvent{})
	if filters.Provider != "" {
		q = q.Where("provider", filters.Provider)
	}
	if filters.EventType != "" {
		q = q.Where("event_type", filters.EventType)
	}
	switch filters.Processed {
	case AdminWebhookProcessedYes:
		q = q.Where("processed_at IS NOT NULL").Where("processing_error IS NULL")
	case AdminWebhookProcessedNo:
		q = q.Where("processed_at IS NULL")
	case AdminWebhookProcessedError:
		q = q.Where("processing_error IS NOT NULL")
	}
	if filters.TransactionID != 0 {
		q = q.Where("transaction_id", filters.TransactionID)
	}
	if filters.ExternalRef != "" {
		q = q.Where("external_ref", filters.ExternalRef)
	}
	if filters.From != nil {
		q = q.Where("received_at >= ?", *filters.From)
	}
	if filters.To != nil {
		q = q.Where("received_at < ?", *filters.To)
	}

	column, dir := webhookEventSort.parse(filters.Sort)
	q = q.OrderBy(column, dir).OrderBy("id", dir)

	var events []models.WebhookEvent
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &events, &total); err != nil {
		return nil, 0, fmt.Errorf("webhook event repo: admin paginate: %w", err)
	}
	return events, total, nil
}

var webhookEventSort = sortable{
	columns: map[string]string{"received_at": "received_at", "processed_at": "processed_at"},
	column:  "received_at",
	dir:     "desc",
}

func (r *webhookEventRepository) FindUnprocessed(olderThan time.Time, limit int) ([]models.WebhookEvent, error) {
	var events []models.WebhookEvent
	err := facades.Orm().Query().
		Where("processed_at IS NULL").
		Where("received_at < ?", olderThan).
		OrderBy("id").
		Limit(limit).
		Find(&events)
	if err != nil {
		return nil, fmt.Errorf("webhook event repo: find unprocessed: %w", err)
	}
	return events, nil
}

func (r *webhookEventRepository) LastReceivedAt(provider string) (*time.Time, error) {
	event, err := first[models.WebhookEvent](facades.Orm().Query().
		Where("provider", provider).
		OrderBy("received_at", "desc").
		OrderBy("id", "desc"), "webhook event repo: last received")
	if err != nil || event == nil {
		return nil, err
	}
	at := event.ReceivedAt
	return &at, nil
}

func (r *webhookEventRepository) UpdateTx(q orm.Query, event *models.WebhookEvent) error {
	if err := q.Save(event); err != nil {
		return fmt.Errorf("webhook event repo: update: %w", err)
	}
	return nil
}

// isUniqueViolation returns true when err is a Postgres unique-constraint
// violation. We rely on the error string because the framework doesn't expose
// the underlying *pq.Error.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// gorm/postgres wraps the driver error; common substrings are sufficient.
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "SQLSTATE 23505")
}
