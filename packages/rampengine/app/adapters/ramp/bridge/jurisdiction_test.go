package bridge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

func TestAdapter_RailsFor(t *testing.T) {
	cpf := ramp.TaxIdentity{Type: "cpf", Country: "BRA"}
	passport := ramp.TaxIdentity{Type: "passport", Country: "ARG"}
	ssn := ramp.TaxIdentity{Type: "ssn", Country: "USA"}
	itin := ramp.TaxIdentity{Type: "itin", Country: "USA"}
	usd := ramp.USDRails()
	with := func(pix ...ramp.Rail) []ramp.Rail { return append(pix, usd...) }
	both := []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
	payout := []ramp.Rail{ramp.RailPixPayout}

	tests := []struct {
		name      string
		identity  ramp.TaxIdentity
		residence string
		want      []ramp.Rail
	}{
		{"passport abroad opens payout only, and usd", passport, "ARG", with(ramp.RailPixPayout)},
		{"cpf abroad opens both, and usd", cpf, "GBR", with(both...)},
		{"cpf in Brazil opens both, and usd", cpf, "BRA", with(both...)},
		{"alpha-2 residence is normalised", cpf, "gb", with(both...)},
		// The EEA asks, by residence, for what the form does not collect yet.
		{"cpf in the eea", cpf, "PRT", nil},
		{"passport in the eea", passport, "FRA", nil},
		{"alpha-2 eea residence", cpf, "de", nil},
		{"an outermost region of the eea", cpf, "REU", nil},
		{"europe outside the eea is served", passport, "CHE", with(ramp.RailPixPayout)},
		{"prohibited residence", cpf, "CUB", nil},
		{"pix not offered in residence", cpf, "JPN", nil},
		// The United States: an SSN or an ITIN opens the payout; Bridge grants
		// pix_onramp to no non-Brazilian individual, and refuses a CPF or a
		// passport as a US resident's tax id (spec §8.1, §8.3).
		{"ssn in the united states opens payout only, and usd", ssn, "USA", with(ramp.RailPixPayout)},
		{"itin in the united states, alpha-2", itin, "us", with(ramp.RailPixPayout)},
		{"cpf in the united states", cpf, "USA", nil},
		{"passport in the united states", passport, "USA", nil},
		{"ssn anywhere else", ssn, "ARG", nil},
		{"ssn in brazil", ssn, "BRA", nil},
		{"passport in a refused residence", passport, "RUS", nil},
		{"unknown residence", cpf, "XXX", nil},
		// No US ACH/FedWire there: Pix stays, USD never comes.
		{"passport where bridge has no usd", passport, "PAK", payout},
		{"cpf where bridge has no usd", cpf, "KEN", both},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, (&Adapter{}).RailsFor(tc.identity, tc.residence))
		})
	}
}

// Every residence without US ACH/FedWire keeps its Pix rails and no USD one.
func TestAdapter_RailsForGivesNoUSDWhereBridgeHasNone(t *testing.T) {
	passport := ramp.TaxIdentity{Type: "passport", Country: "ARG"}
	for code := range usdUnservedResidences {
		t.Run(code, func(t *testing.T) {
			for _, rail := range (&Adapter{}).RailsFor(passport, code) {
				assert.NotContains(t, ramp.USDRails(), rail)
			}
		})
	}
}

var _ ramp.JurisdictionPolicy = (*Adapter)(nil)

// Every code in a refused set is an ISO country, spelled as the policy
// compares it, and refused: a typo in a sanctions list would serve that
// country without a word, since the lookup would simply never match.
func TestAdapter_RailsForRefusesEveryListedResidence(t *testing.T) {
	cpf := ramp.TaxIdentity{Type: "cpf", Country: "BRA"}
	adapter := &Adapter{}
	for name, set := range map[string]map[string]struct{}{"refused": refusedResidences, "eea": eeaResidences} {
		for code := range set {
			t.Run(name+"/"+code, func(t *testing.T) {
				alpha3, ok := iso3166.Alpha3(code)
				assert.True(t, ok, "%s is not an ISO country", code)
				assert.Equal(t, code, alpha3, "listed as the policy compares it")
				assert.Empty(t, adapter.RailsFor(cpf, code))
			})
		}
	}
	assert.NotEmpty(t, adapter.RailsFor(cpf, "ARG"), "control: a residence on no list is served")
}

// The EEA set is exactly what Bridge publishes on its EEA requirements page
// (checked 2026-10-07; Åland has no code of its own there): a missing code
// serves a resident Bridge will leave incomplete, an extra one refuses a
// served country.
func TestEEAResidencesAreExactlyWhatBridgePublishes(t *testing.T) {
	published := strings.Split("AUT, BEL, BGR, CYP, CZE, DEU, DNK, ESP, EST, FIN, FRA, GLP, GUF, MAF, MTQ, MYT, REU, GRC, HRV, HUN, IRL, ISL, ITA, LIE, LTU, LUX, LVA, MLT, NLD, NOR, POL, PRT, ROU, SVK, SVN, SWE", ", ")
	assert.Len(t, published, 36)
	want := map[string]struct{}{}
	for _, code := range published {
		want[code] = struct{}{}
	}
	assert.Equal(t, want, eeaResidences)

	adapter := &Adapter{}
	assert.Empty(t, adapter.RailsFor(ramp.TaxIdentity{Type: "cpf", Country: "BRA"}, "ESP"))
	assert.Empty(t, adapter.RailsFor(ramp.TaxIdentity{Type: "passport", Country: "ESP"}, "ESP"))
}

// A company is judged by its own identity and its country of incorporation,
// never by whoever registers it (spec §7.2): a CNPJ opens both Pix rails; a
// company from abroad only the deposit, which the router still gates on the
// pix_onramp endorsement, and never the withdrawal, which a company makes
// only to its own CNPJ key (§18.8). The EEA, the refused countries and a tax
// id the country does not issue get nothing.
func TestAdapter_RailsForACompany(t *testing.T) {
	both := []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout}
	payin := []ramp.Rail{ramp.RailPixPayin}
	company := func(typ, country string) ramp.TaxIdentity { return ramp.TaxIdentity{Type: typ, Country: country} }

	tests := []struct {
		name          string
		identity      ramp.TaxIdentity
		incorporation string
		want          []ramp.Rail
	}{
		{"cnpj in brazil", company("cnpj", "BRA"), "BRA", both},
		{"cnpj, alpha-2", company("cnpj", "BRA"), "br", both},
		{"ein in the united states", company("ein", "USA"), "USA", payin},
		{"crn in the united kingdom", company("crn", "GBR"), "GBR", payin},
		{"vat in the united kingdom", company("vat", "GBR"), "GBR", payin},
		{"rfc in mexico", company("rfc", "MEX"), "MEX", payin},
		{"vat in argentina", company("vat", "ARG"), "ARG", payin},
		{"other in argentina", company("other", "ARG"), "ARG", payin},
		{"a cnpj abroad is no company's", company("cnpj", "BRA"), "ARG", nil},
		{"an ein outside the united states", company("ein", "USA"), "GBR", nil},
		{"vat in brazil", company("vat", "BRA"), "BRA", nil},
		{"incorporated in the eea", company("vat", "DEU"), "DEU", nil},
		{"incorporated in a prohibited country", company("vat", "RUS"), "RUS", nil},
		{"incorporated where bridge offers no pix", company("vat", "JPN"), "JPN", nil},
		{"unknown country", company("vat", "XXX"), "XXX", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, (&Adapter{}).RailsFor(tc.identity, tc.incorporation))
		})
	}
}
