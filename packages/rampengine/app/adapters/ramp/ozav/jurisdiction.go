package ozav

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// refusedJurisdictions are the countries Ozav does not serve, as residence or
// as the country that issued the tax id: openapi 2.0.0 "Jurisdictions we
// serve", https://developers.ozav.io/openapi.json (checked 2026-10-07).
var refusedJurisdictions = map[string]struct{}{
	"AFG": {}, "BLR": {}, "CUB": {}, "IRN": {}, "MMR": {}, "PRK": {},
	"RUS": {}, "SDN": {}, "SSD": {}, "SYR": {}, "ZWE": {},
}

// RailsFor is what Ozav opens: both Pix rails for a CPF holder living in
// Brazil, nothing for anyone else. A passport gets no Brazilian account, and
// a CPF abroad would fall in Ozav's Brazilian profile, which demands
// neighbourhood and state the generic form does not collect.
func (a *Adapter) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	res, ok := iso3166.Alpha3(residence)
	if !ok || isRefused(res) {
		return nil
	}
	if country, ok := iso3166.Alpha3(identity.Country); !ok || isRefused(country) {
		return nil
	}
	if identity.Type == models.TaxIDTypeCPF && res == "BRA" {
		return ramp.PixRails()
	}
	return nil
}

func isRefused(alpha3 string) bool {
	_, ok := refusedJurisdictions[alpha3]
	return ok
}
