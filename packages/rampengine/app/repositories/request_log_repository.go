package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RequestLogRepository persists HTTP request captures.
type RequestLogRepository interface {
	CreateBatch(ctx context.Context, rows []models.RequestLog) error
	FindByID(id uint) (*models.RequestLog, error)
	AdminPaginate(filters AdminRequestLogListFilters) ([]models.RequestLog, int64, error)
	PurgeOlderThan(cutoff time.Time) (int64, error)
}

// AdminRequestLogListFilters scopes the admin request-log explorer.
type AdminRequestLogListFilters struct {
	Audience  string
	Method    string
	Path      string
	Status    int
	StatusMin int
	StatusMax int
	UserID    uint
	AdminID   uint
	RequestID string
	From      *time.Time
	To        *time.Time
	Page      int
	PerPage   int
	Sort      string
}

type requestLogRepository struct{}

func NewRequestLogRepository() RequestLogRepository {
	return &requestLogRepository{}
}

func (r *requestLogRepository) CreateBatch(_ context.Context, rows []models.RequestLog) error {
	if len(rows) == 0 {
		return nil
	}
	if err := facades.Orm().Query().Create(&rows); err != nil {
		return fmt.Errorf("request log repo: create batch: %w", err)
	}
	return nil
}

func (r *requestLogRepository) FindByID(id uint) (*models.RequestLog, error) {
	return first[models.RequestLog](facades.Orm().Query().Where("id", id), "request log repo: find by id")
}

func (r *requestLogRepository) AdminPaginate(filters AdminRequestLogListFilters) ([]models.RequestLog, int64, error) {
	q := facades.Orm().Query().Model(&models.RequestLog{})
	if filters.Audience != "" {
		q = q.Where("audience", filters.Audience)
	}
	if filters.Method != "" {
		q = q.Where("method", filters.Method)
	}
	if filters.Path != "" {
		q = q.Where("path ILIKE ?", likeContains(filters.Path))
	}
	if filters.Status != 0 {
		q = q.Where("status", filters.Status)
	}
	if filters.StatusMin != 0 {
		q = q.Where("status >= ?", filters.StatusMin)
	}
	if filters.StatusMax != 0 {
		q = q.Where("status <= ?", filters.StatusMax)
	}
	if filters.UserID != 0 {
		q = q.Where("user_id", filters.UserID)
	}
	if filters.AdminID != 0 {
		q = q.Where("admin_id", filters.AdminID)
	}
	if filters.RequestID != "" {
		q = q.Where("request_id", filters.RequestID)
	}
	if filters.From != nil {
		q = q.Where("created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		q = q.Where("created_at < ?", *filters.To)
	}

	column, dir := requestLogSort.parse(filters.Sort)
	q = q.OrderBy(column, dir).OrderBy("id", dir)

	var rows []models.RequestLog
	var total int64
	if err := q.Paginate(filters.Page, filters.PerPage, &rows, &total); err != nil {
		return nil, 0, fmt.Errorf("request log repo: admin paginate: %w", err)
	}
	return rows, total, nil
}

var requestLogSort = sortable{
	columns: map[string]string{"created_at": "created_at"},
	column:  "created_at",
	dir:     "desc",
}

func (r *requestLogRepository) PurgeOlderThan(cutoff time.Time) (int64, error) {
	res, err := facades.Orm().Query().
		Where("created_at < ?", cutoff).
		Delete(&models.RequestLog{})
	if err != nil {
		return 0, fmt.Errorf("request log repo: purge: %w", err)
	}
	return res.RowsAffected, nil
}
