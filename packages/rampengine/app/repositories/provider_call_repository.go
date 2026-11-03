package repositories

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ProviderCallRepository is the persistence boundary for a user's provider
// timeline.
//
// There is no Update and no Delete. A row is a fact about something that
// already happened, and a timeline whose past can be edited answers nothing —
// the whole reason it exists is that a log line was gone by the time anyone
// asked. Retention prunes by age, which is a different act from rewriting.
type ProviderCallRepository interface {
	Create(call *models.ProviderCall) error
	// ListByCustomer returns a customer's timeline — every call made for it,
	// whoever made it — newest first, bounded by limit.
	ListByCustomer(customerID uint, limit int) ([]models.ProviderCall, error)
	// DeleteOlderThan prunes by age and answers how many rows went. Retention
	// is the only way rows leave this table.
	DeleteOlderThan(days int) (int64, error)
}

type providerCallRepository struct{}

func NewProviderCallRepository() ProviderCallRepository { return &providerCallRepository{} }

func (r *providerCallRepository) Create(call *models.ProviderCall) error {
	if err := facades.Orm().Query().Create(call); err != nil {
		return fmt.Errorf("provider calls: create: %w", err)
	}
	return nil
}

func (r *providerCallRepository) ListByCustomer(customerID uint, limit int) ([]models.ProviderCall, error) {
	if limit <= 0 {
		limit = 100
	}
	var calls []models.ProviderCall
	if err := facades.Orm().Query().
		Where("customer_id", customerID).
		OrderByDesc("created_at").
		OrderByDesc("id").
		Limit(limit).
		Find(&calls); err != nil {
		return nil, fmt.Errorf("provider calls: list for customer %d: %w", customerID, err)
	}
	return calls, nil
}

func (r *providerCallRepository) DeleteOlderThan(days int) (int64, error) {
	res, err := facades.Orm().Query().
		Where("created_at < NOW() - MAKE_INTERVAL(days => ?)", days).
		Delete(&models.ProviderCall{})
	if err != nil {
		return 0, fmt.Errorf("provider calls: prune older than %d days: %w", days, err)
	}
	return res.RowsAffected, nil
}
