package repositories

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RampEstimateRepository stores the quotes a ramp order is priced from.
type RampEstimateRepository interface {
	Create(estimate *models.RampEstimate) error
	// FindByCustomerAndExternalID scopes the lookup to the customer the quote
	// prices for: the external id is what the client sends back, so an
	// unscoped read would let one customer price an order from another's quote
	// — a person from their company's, among others. Returns (nil, nil) when
	// absent.
	FindByCustomerAndExternalID(customerID uint, externalID string) (*models.RampEstimate, error)
	// FindByCustomerAndID loads the quote row an order was priced from. Scoped
	// to the customer for the same reason as FindByCustomerAndExternalID.
	FindByCustomerAndID(customerID, id uint) (*models.RampEstimate, error)
	// FindByCustomerAndIDs batch-loads quote rows for list projection.
	FindByCustomerAndIDs(customerID uint, ids []uint) (map[uint]models.RampEstimate, error)
	// MarkConsumed stamps consumed_at, which is what makes a binding quote
	// single-use. Consuming an already-consumed estimate is refused.
	MarkConsumed(estimate *models.RampEstimate) error
}

type rampEstimateRepository struct{}

// NewRampEstimateRepository constructs the repository.
func NewRampEstimateRepository() RampEstimateRepository { return &rampEstimateRepository{} }

func (r *rampEstimateRepository) Create(estimate *models.RampEstimate) error {
	if err := facades.Orm().Query().Create(estimate); err != nil {
		return fmt.Errorf("ramp estimate repo: create: %w", err)
	}
	return nil
}

func (r *rampEstimateRepository) FindByCustomerAndExternalID(customerID uint, externalID string) (*models.RampEstimate, error) {
	return first[models.RampEstimate](facades.Orm().Query().
		Where("customer_id = ?", customerID).
		Where("external_id = ?", externalID), "ramp estimate repo: find")
}

func (r *rampEstimateRepository) FindByCustomerAndID(customerID, id uint) (*models.RampEstimate, error) {
	return first[models.RampEstimate](facades.Orm().Query().
		Where("customer_id = ?", customerID).
		Where("id = ?", id), "ramp estimate repo: find by id")
}

func (r *rampEstimateRepository) FindByCustomerAndIDs(customerID uint, ids []uint) (map[uint]models.RampEstimate, error) {
	if len(ids) == 0 {
		return map[uint]models.RampEstimate{}, nil
	}
	var rows []models.RampEstimate
	if err := facades.Orm().Query().
		Where("customer_id", customerID).
		Where("id IN ?", ids).
		Find(&rows); err != nil {
		return nil, fmt.Errorf("ramp estimate repo: find by ids: %w", err)
	}
	out := make(map[uint]models.RampEstimate, len(rows))
	for i := range rows {
		out[rows[i].ID] = rows[i]
	}
	return out, nil
}

func (r *rampEstimateRepository) MarkConsumed(estimate *models.RampEstimate) error {
	now := time.Now()
	estimate.ConsumedAt = &now
	if err := facades.Orm().Query().Save(estimate); err != nil {
		return fmt.Errorf("ramp estimate repo: mark consumed: %w", err)
	}
	return nil
}
