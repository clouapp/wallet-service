package rules

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// BridgePostalCodeRule validates a residential postal code against the format
// of the country it is in (iso3166.PostalCodePattern, the list Bridge checks
// addresses against). A country that issues no postal code takes anything:
// the form request does not require one there.
type BridgePostalCodeRule struct{}

func (r *BridgePostalCodeRule) Signature() string { return "bridge_postal_code" }

func (r *BridgePostalCodeRule) Passes(_ context.Context, data validation.Data, val any, options ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	return ValidBridgePostalCode(s, kycCountryFromData(data, options))
}

// ValidBridgePostalCode is the bridge_postal_code rule for an address in
// country (empty is Brazil). The code is read as the KYC service sends it,
// trimmed and in capitals (CanonicalPostalCode): the UK, Canadian, Dutch and
// Irish patterns only match capitals, and "sw1a 1aa" is the same code.
func ValidBridgePostalCode(postalCode, country string) bool {
	return iso3166.ValidPostalCode(models.ResidenceCode(country), CanonicalPostalCode(postalCode))
}

// CanonicalPostalCode is a postal code as it is validated, stored and sent:
// trimmed and in capitals.
func CanonicalPostalCode(postalCode string) string {
	return strings.ToUpper(strings.TrimSpace(postalCode))
}

// PostalCodeRequired reports whether an address in country (empty is Brazil)
// must carry a postal code: wherever the country issues one.
func PostalCodeRequired(country string) bool {
	return iso3166.PostalCodePattern(models.ResidenceCode(country)) != ""
}

func (r *BridgePostalCodeRule) Message(_ context.Context) string {
	return "must be a valid postal code for the selected country"
}
