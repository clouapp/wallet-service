package repositories

import (
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// AdminAuditLogRepository persists the admin audit trail. It only appends and
// reads: nothing updates or deletes an entry.
type AdminAuditLogRepository interface {
	Create(entry *models.AdminAuditLog) error
	// CreateTx appends the entry through query, so a repository that writes a
	// state change in a transaction lands its audit row in the same one.
	CreateTx(query orm.Query, entry *models.AdminAuditLog) error
	// Paginate lists entries newest first, each with its admin loaded, trashed
	// admins included.
	Paginate(filters AdminAuditLogListFilters) ([]models.AdminAuditLog, int64, error)
}

// AdminAuditLogListFilters scopes an audit listing. Zero values do not filter.
// To is exclusive.
type AdminAuditLogListFilters struct {
	AdminID     uint
	SubjectType string
	// SubjectTypes is any of these subject types: one subject's history
	// recorded under more than one, a person's account and customer.
	SubjectTypes []string
	SubjectID    uint
	Action       string
	From         *time.Time
	To           *time.Time
	Page         int
	PerPage      int
}

type adminAuditLogRepository struct{}

// NewAdminAuditLogRepository returns an AdminAuditLogRepository backed by the
// default ORM connection.
func NewAdminAuditLogRepository() AdminAuditLogRepository {
	return &adminAuditLogRepository{}
}

func (r *adminAuditLogRepository) Create(entry *models.AdminAuditLog) error {
	return r.CreateTx(facades.Orm().Query(), entry)
}

func (r *adminAuditLogRepository) CreateTx(query orm.Query, entry *models.AdminAuditLog) error {
	if err := createAuditLog(query, entry); err != nil {
		return fmt.Errorf("admin audit log repo: create: %w", err)
	}
	return nil
}

// createAuditLog is the insert every repository in this package uses to land
// an audit row inside its own transaction.
func createAuditLog(query orm.Query, entry *models.AdminAuditLog) error {
	// The column is NOT NULL jsonb, which refuses an empty string.
	if entry.Metadata == "" {
		entry.Metadata = "{}"
	}
	return query.Create(entry)
}

func (r *adminAuditLogRepository) Paginate(filters AdminAuditLogListFilters) ([]models.AdminAuditLog, int64, error) {
	q := facades.Orm().Query().Model(&models.AdminAuditLog{}).
		With("Admin", func(admins orm.Query) orm.Query { return admins.WithTrashed() })
	if filters.AdminID != 0 {
		q = q.Where("admin_id", filters.AdminID)
	}
	if filters.SubjectType != "" {
		q = q.Where("subject_type", filters.SubjectType)
	}
	if len(filters.SubjectTypes) > 0 {
		q = q.Where("subject_type IN ?", filters.SubjectTypes)
	}
	if filters.SubjectID != 0 {
		q = q.Where("subject_id", filters.SubjectID)
	}
	if filters.Action != "" {
		q = q.Where("action", filters.Action)
	}
	if filters.From != nil {
		q = q.Where("created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		q = q.Where("created_at < ?", *filters.To)
	}
	q = q.OrderBy("created_at", "desc").OrderBy("id", "desc")

	var rows []models.AdminAuditLog
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &rows, &total); err != nil {
		return nil, 0, fmt.Errorf("admin audit log repo: paginate: %w", err)
	}
	return rows, total, nil
}
