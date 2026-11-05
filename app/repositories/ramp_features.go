package repositories

import (
	"context"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
)

type RampFeature = models.RampFeature

func (r *RampCustomers) FeatureSubject(ctx context.Context, contactID uint) (uuid.UUID, uuid.UUID, error) {
	var row struct {
		ID        uuid.UUID
		AccountID uuid.UUID
	}
	err := r.Query(ctx).Table("ramp_customers").Where("engine_contact_id", contactID).First(&row)
	return row.ID, row.AccountID, err
}
func (r *RampCustomers) FeatureOverrides(ctx context.Context, customerID uuid.UUID) ([]RampFeature, error) {
	var rows []RampFeature
	err := r.Query(ctx).Table("ramp_customer_features").Where("customer_id", customerID).Get(&rows)
	return rows, err
}
func (r *RampCustomers) SetFeature(ctx context.Context, customerID uuid.UUID, key string, value bool) error {
	_, err := r.Query(ctx).Exec("INSERT INTO ramp_customer_features(customer_id,feature,enabled) VALUES(?,?,?) ON CONFLICT(customer_id,feature) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=now()", customerID, key, value)
	return err
}
