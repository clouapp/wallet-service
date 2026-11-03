package kyc

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RefreshEnabled asks every provider still deciding about the customer for its
// answer, then folds the rows into the overview — the app's "Atualizar status".
//
// Only the providers the overview already lists (enabled and serving the user)
// are asked, and of those only one with something pending upstream: a row the
// user submitted that is not approved, not refused for good, and not still on
// its way. Approved rows ask again only while a Pix endorsement is under
// review. A provider that fails is logged and left as it was: one slow
// provider must not hide what the others answered.
func (s *ProviderKycService) RefreshEnabled(ctx context.Context, customerID uint) (*OverviewView, error) {
	rows, _, err := s.list(customerID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		row := &rows[i]
		if !awaitsProvider(row) || s.staging.InFlightKind(customerID, row.Provider) != "" {
			continue
		}
		if _, err := s.Refresh(ctx, customerID, row.Provider); err != nil {
			if errors.Is(err, ramp.ErrIntegrationRequired) || errors.Is(err, ramp.ErrProviderNotImplemented) {
				continue
			}
			facades.Log().WithContext(ctx).Warningf("kyc: refresh %s for customer %d failed: %v", row.Provider, customerID, err)
		}
	}
	return s.Overview(customerID)
}

// awaitsProvider reports whether the row holds a decision still to come from
// the provider.
func awaitsProvider(row *models.CustomerProviderKyc) bool {
	if row.ID == 0 || Terminal(row) {
		return false
	}
	switch row.KycStatus {
	case models.KycStatusIncomplete, models.KycStatusUnderReview, models.KycStatusPaused, models.KycStatusRequiresInfo:
		return true
	case models.KycStatusApproved:
		return row.AwaitingPixEndorsementReview()
	}
	return false
}
