package ozav

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

func TestAdapter_RailsFor(t *testing.T) {
	cpf := ramp.TaxIdentity{Type: "cpf", Country: "BRA"}
	passport := ramp.TaxIdentity{Type: "passport", Country: "ARG"}

	tests := []struct {
		name      string
		identity  ramp.TaxIdentity
		residence string
		want      []ramp.Rail
	}{
		{"cpf in Brazil opens both", cpf, "BRA", []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}},
		{"alpha-2 residence is normalised", cpf, "br", []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}},
		{"cpf abroad is not served", cpf, "PRT", nil},
		{"passport is not served", passport, "ARG", nil},
		{"refused residence", cpf, "RUS", nil},
		{"refused identity country", ramp.TaxIdentity{Type: "passport", Country: "IRN"}, "BRA", nil},
		{"unknown residence", cpf, "XXX", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, (&Adapter{}).RailsFor(tc.identity, tc.residence))
		})
	}
}

var _ ramp.JurisdictionPolicy = (*Adapter)(nil)

// Every refused code is an ISO country, spelled as the policy compares it,
// and refused. Ozav serves only Brazil as a residence, so the list is read
// through the tax id's issuing country: a typo there would go unnoticed.
func TestAdapter_RailsForRefusesEveryListedJurisdiction(t *testing.T) {
	adapter := &Adapter{}
	for code := range refusedJurisdictions {
		t.Run(code, func(t *testing.T) {
			alpha3, ok := iso3166.Alpha3(code)
			assert.True(t, ok, "%s is not an ISO country", code)
			assert.Equal(t, code, alpha3, "listed as the policy compares it")
			assert.Empty(t, adapter.RailsFor(ramp.TaxIdentity{Type: "cpf", Country: code}, "BRA"))
		})
	}
	assert.NotEmpty(t, adapter.RailsFor(ramp.TaxIdentity{Type: "cpf", Country: "BRA"}, "BRA"),
		"control: a tax id issued by a country on no list is served")
}

// Ozav takes no company until its own KYB spec (KYB spec §15): a CNPJ gets
// nothing, in Brazil or anywhere.
func TestRailsFor_ACompanyGetsNothing(t *testing.T) {
	for _, identity := range []ramp.TaxIdentity{{Type: "cnpj", Country: "BRA"}, {Type: "vat", Country: "ARG"}} {
		assert.Empty(t, (&Adapter{}).RailsFor(identity, identity.Country), identity.Type)
	}
}
