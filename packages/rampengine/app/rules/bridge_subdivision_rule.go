package rules

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// BridgeSubdivisionRule validates a residential subdivision for the country
// it is in. Brazil takes a state (its code or its name); a country whose
// addresses must name one (iso3166.RequiredSubdivisions) takes a code from
// its list, as Bridge spells it; anywhere else the value is ignored — Bridge
// takes those addresses without one, and the form does not ask for it.
type BridgeSubdivisionRule struct{}

func (r *BridgeSubdivisionRule) Signature() string { return "bridge_subdivision" }

func (r *BridgeSubdivisionRule) Passes(_ context.Context, data validation.Data, val any, options ...any) bool {
	s, ok := val.(string)
	if !ok {
		return false
	}
	return ValidBridgeSubdivision(s, kycCountryFromData(data, options))
}

// ValidBridgeSubdivision is the bridge_subdivision rule for an address in
// country (empty is Brazil).
func ValidBridgeSubdivision(subdivision, country string) bool {
	subdivision = strings.TrimSpace(subdivision)
	country = models.ResidenceCode(country)
	if country == "BRA" {
		_, known := brazil.SubdivisionCode(subdivision)
		return known
	}
	listed := iso3166.RequiredSubdivisions(country)
	if listed == nil {
		return true
	}
	for _, s := range listed {
		if s.Code == subdivision {
			return true
		}
	}
	return false
}

// SubdivisionRequired reports whether an address in country (empty is
// Brazil) must name a subdivision: the state in Brazil, and one from the list
// wherever the country has one.
func SubdivisionRequired(country string) bool {
	country = models.ResidenceCode(country)
	return country == "BRA" || iso3166.RequiredSubdivisions(country) != nil
}

func (r *BridgeSubdivisionRule) Message(_ context.Context) string {
	return "must be a valid state or subdivision for the selected country"
}
