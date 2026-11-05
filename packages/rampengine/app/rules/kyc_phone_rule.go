package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// KycPhoneRule accepts any phone the KYC contract can canonicalise into
// E.164 for the country the same request declares — a bare national number
// ("11987654321", which is what the Android app posts), one written with
// human separators, or one already in E.164.
//
// It exists so a number nothing can canonicalise is refused here, naming
// `phone`, instead of downstream: an adapter that models the calling code
// separately would otherwise submit a customer with a blank country_code and
// the user would read a provider's error about a field they never filled in.
//
// The rule and the canonicalisation in the KYC service share
// ramp.NormalizePhone on purpose: what validates and what converts must be
// the same function, or the boundary accepts values the conversion cannot
// take.
type KycPhoneRule struct{}

func (r *KycPhoneRule) Signature() string { return "kyc_phone" }

func (r *KycPhoneRule) Passes(_ context.Context, data validation.Data, val any, options ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	return ValidKycPhone(s, kycCountryFromData(data, options))
}

// ValidKycPhone is the kyc_phone rule for a phone declared with country: one
// ramp.NormalizePhone can canonicalise. Empty passes — phone is optional, and
// NormalizePhone agrees by normalising it to empty.
func ValidKycPhone(phone, country string) bool {
	_, err := ramp.NormalizePhone(phone, models.ResidenceCode(country))
	return err == nil
}

func (r *KycPhoneRule) Message(_ context.Context) string {
	return "must be a phone number that can be read for the selected country, or be in international +55… form"
}
