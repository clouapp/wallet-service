package kyc

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	kycflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// residenceOpen reports whether new KYC may declare residence. One no flag
// holds (kycflags.ResidenceFlag) is open. A held one is open while its flag is
// on; a service wired without flags keeps it shut, and a failed read is an
// error, never open.
func (s *ProviderKycService) residenceOpen(ctx context.Context, residence string) (bool, error) {
	code, ok := iso3166.Alpha3(residence)
	if !ok {
		return true, nil
	}
	flag, held := kycflags.ResidenceFlag(code)
	if !held {
		return true, nil
	}
	if s.features == nil {
		return false, nil
	}
	on, err := s.features.IsActive(ctx, flag, nil)
	if err != nil {
		return false, fmt.Errorf("kyc: read %s: %w", flag, err)
	}
	return on, nil
}

// refuseClosedResidence answers ErrJurisdictionNotServed for a person whose
// residence a flag holds shut: the refusal a residence nobody serves gets,
// before anything is staged or a provider asked. Nobody in particular (nil)
// declares no residence and is never refused here, and neither is a company:
// its country of incorporation is no one's residence, and the flags wait on
// what the person's form shows (KYC USA spec R2), not on a company's (PLANO
// B2). What a provider refuses a company is still refused, by its policy.
func (s *ProviderKycService) refuseClosedResidence(ctx context.Context, who *ramp.Person) error {
	if who == nil || who.Identity.IsBusiness() {
		return nil
	}
	open, err := s.residenceOpen(ctx, who.Residence)
	if err != nil {
		return err
	}
	if !open {
		return ErrJurisdictionNotServed
	}
	return nil
}
