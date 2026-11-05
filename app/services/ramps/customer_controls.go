package ramps

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/activity"
	"github.com/macrowallets/waas/app/services/features"
	em "github.com/macrowallets/waas/packages/rampengine/app/models"
	er "github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

var customerFeatures = []string{"ramp-payin-enabled", "ramp-payout-enabled", "ramp-usd-payin-enabled", "ramp-usd-payout-enabled", "ramp-bank-payout-enabled", "kyb"}

type CustomerFeature struct {
	Key       string
	Effective bool
	Override  *bool
	Label     string
}

func (s *Service) CustomerFeatures(ctx context.Context, customer *models.RampCustomer) ([]CustomerFeature, error) {
	out := []CustomerFeature{}
	overrides, err := s.d.Customers.FeatureOverrides(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	flags := Flags{Native: s.d.Features, AccountID: customer.AccountID, Customers: s.d.Customers}
	for _, key := range customerFeatures {
		d, _ := features.Find(key)
		effective, err := flags.IsActive(ctx, key, em.UserFlagScope(customer.EngineContactID))
		if err != nil {
			return nil, err
		}
		row := CustomerFeature{Key: key, Label: d.Label, Effective: effective}
		for _, override := range overrides {
			if override.Feature == key {
				v := override.Enabled
				row.Override = &v
			}
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *Service) SetCustomerFeature(ctx context.Context, actorID uuid.UUID, customer *models.RampCustomer, key string, value bool) error {
	allowed := false
	for _, k := range customerFeatures {
		allowed = allowed || k == key
	}
	if !allowed {
		return fmt.Errorf("unknown customer feature")
	}
	if actorID == uuid.Nil || s.d.Activity == nil {
		return ErrFundingForbidden
	}
	return s.d.Activity.Within(ctx, func(ctx context.Context) error {
		if err := s.d.Customers.SetFeature(ctx, customer.ID, key, value); err != nil {
			return err
		}
		metadata, err := activity.FeatureChange(key, value)
		if err != nil {
			return err
		}
		return s.d.Activity.Append(ctx, models.AccountActivity{AccountID: &customer.AccountID, ActorUserID: actorID, Action: activity.ActionFeaturesUpdated, TargetType: activity.TargetFeature, TargetID: customer.ID.String() + ":" + key, Metadata: metadata})
	})
}
func (s *Service) CustomerWallets(ctx context.Context, customer *models.RampCustomer) ([]em.CustomerWallet, error) {
	return er.NewCustomerWalletRepository().ListByCustomers([]uint{customer.EngineCustomerID})
}
