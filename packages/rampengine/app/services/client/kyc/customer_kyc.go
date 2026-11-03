package kyc

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// RefuseUnservedBusiness answers ErrJurisdictionNotServed for a company no
// enabled provider serves: one with a tax identity no provider taking
// companies opens a rail to where it was incorporated (the EEA, the refused
// countries, a country with no business provider). A residence flag holds no
// company (refuseClosedResidence). It reads the providers the countries catalog does, so a company
// is refused exactly where the catalog lists no business_tax_identities.
// The incorporation country is the company's residence (spec §7.2).
func (s *ProviderKycService) RefuseUnservedBusiness(ctx context.Context, identity models.TaxIdentity) error {
	adapters, err := implementedProviders(s.config, s.factory, s.appEnv, nil)
	if err != nil {
		return err
	}
	company := &ramp.Person{Identity: identity, Residence: identity.Country}
	if err := s.refuseClosedResidence(ctx, company); err != nil {
		return err
	}
	served, err := serving(adapters, company, pixRailLister(s.config))
	if err != nil {
		return err
	}
	if len(served) == 0 {
		return ErrJurisdictionNotServed
	}
	return nil
}

// CustomerKyc is the KYC rows customerID holds at the providers serving it,
// each marked as GET /api/kyc marks it (RailsNotEligible): what GET
// /api/customers summarises. A provider the customer never touched has no row
// here, and none is made up for it.
func (s *ProviderKycService) CustomerKyc(customerID uint) ([]models.CustomerProviderKyc, error) {
	rows, stored, err := s.list(customerID)
	if err != nil {
		return nil, err
	}
	held := make(map[string]bool, len(stored))
	for i := range stored {
		held[stored[i].Provider] = true
	}
	out := make([]models.CustomerProviderKyc, 0, len(stored))
	for i := range rows {
		if held[rows[i].Provider] {
			out = append(out, rows[i])
		}
	}
	return out, nil
}

// RequireApproved answers nil when customerID is approved at a provider, as
// the overview the app reads says it (Overall approved), and otherwise a
// *ramp.KycBlocked: ErrKycRejected when every provider refused it for good,
// ErrKycPending with the steps the overview still opens otherwise. It names
// the provider when one alone serves the customer; with several, the app
// opens the overview.
func (s *ProviderKycService) RequireApproved(customerID uint) error {
	view, err := s.Overview(customerID)
	if err != nil {
		return fmt.Errorf("kyc: require approved: %w", err)
	}
	blocked := &ramp.KycBlocked{Err: ramp.ErrKycPending, PendingSteps: view.Overall.PendingSteps}
	switch view.Overall.Status {
	case OverallApproved:
		return nil
	case OverallRejected:
		blocked.Err, blocked.PendingSteps = ramp.ErrKycRejected, nil
	}
	if len(view.Providers) == 1 {
		blocked.Provider, blocked.Status = view.Providers[0].Provider, view.Providers[0].KycStatus
	}
	return blocked
}
