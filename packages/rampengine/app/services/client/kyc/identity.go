package kyc

import (
	"cmp"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrTaxIDInUse refuses an application declaring a CPF another XIP account
// holds. One CPF is one account (decided on 28/09/2026), and no resubmission
// fixes it, so the application reaches no provider at all: before this check,
// the providers were what found the duplicate, after the whole application
// had been sent to them.
var ErrTaxIDInUse = repositories.ErrTaxIDInUse

// ErrPhoneInUse refuses an application declaring a phone another XIP account
// holds. Unlike the CPF it is a field the user corrects.
var ErrPhoneInUse = repositories.ErrPhoneInUse

// declaredIdentity is the tax id with what it is — its type and the country
// that issued it — and the phone an application stands for. tax is zero when
// there is no tax id.
type declaredIdentity struct {
	taxID string
	tax   models.TaxIdentity
	phone string
}

// declaration is the identity as the profile repository digests it.
func (d declaredIdentity) declaration() ramp.Profile {
	return ramp.Profile{TaxID: d.taxID, TaxIDType: d.tax.Type, TaxIDCountry: d.tax.Country, Phone: d.phone}
}

// checkIdentity refuses an application whose CPF or phone another account
// holds, and answers the identity it stands for: what it carries or, for
// whichever it omits, what the retained profile already declares. A
// resubmission without the CPF is still, to every provider that has it, the
// person with that CPF. A tax id travels with its own identity — tax, the one
// the application declares (taxIdentityOf), or the retained profile's — since
// that, never the residence, is what says whether it is a CPF: a CPF declared
// by someone living abroad is reserved as a CPF. A different identity than
// the retained one is not refused (no tax_id_locked, spec §3.3): CPF to
// passport and back is allowed.
//
// It runs before anything else an application does — before a provider is
// linked, a profile retained, a submission staged — which is the point: the
// refusal leaves nothing behind. phone is the canonical one applicationFrom
// produced, the form the index digests.
//
// Failing to read either side is not passing: the check exists to keep a CPF
// from reaching a provider twice.
func (s *ProviderKycService) checkIdentity(customerID uint, taxID string, tax models.TaxIdentity, phone string) (declaredIdentity, error) {
	identity := declaredIdentity{taxID: canonicalTaxID(taxID, tax), phone: phone}
	if identity.taxID != "" {
		identity.tax = tax
	}
	if identity.taxID == "" || identity.phone == "" {
		retained, err := s.profileRepo.Find(customerID)
		if err != nil {
			return declaredIdentity{}, fmt.Errorf("kyc: read the retained identity: %w", err)
		}
		if retained != nil {
			if identity.taxID == "" && retained.TaxID != "" {
				identity.taxID, identity.tax = retained.TaxID, retained.TaxIdentity()
			}
			identity.phone = cmp.Or(identity.phone, retained.Phone)
		}
	}
	if err := s.profileRepo.CheckIdentityAvailable(customerID, identity.declaration()); err != nil {
		return declaredIdentity{}, fmt.Errorf("kyc: check identity: %w", err)
	}
	return identity, nil
}
