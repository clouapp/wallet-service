package ramp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// servesCompanies is a provider with a jurisdiction policy that also takes a
// business customer.
type servesCompanies struct{ withPolicy }

func (servesCompanies) BusinessApplicationDocuments(string, bool) []string {
	return []string{ramp.CompanyDocumentFormation}
}

// A company is served only by a provider that declares it takes business
// customers (spec §7): one that never heard of a CNPJ would otherwise open
// every rail it Supports to it, the way it opens them to nobody in particular.
func TestRailsOf_ACompanyIsServedOnlyByABusinessKycAdapter(t *testing.T) {
	cnpj := ramp.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}
	supports := supportsOnly{rail: ramp.RailPixPayout, token: models.TargetTokenUSDT}
	payout := []ramp.Rail{ramp.RailPixPayout}

	assert.Empty(t, ramp.RailsOf(supports, cnpj, "BRA"), "no policy, no business support")
	assert.Empty(t, ramp.RailsOf(withPolicy{supportsOnly: supports, rails: payout}, cnpj, "BRA"), "a person's policy only")
	assert.Equal(t, payout, ramp.RailsOf(servesCompanies{withPolicy{supportsOnly: supports, rails: payout}}, cnpj, "BRA"))

	// A person is read as before, business support or not.
	cpf := ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}
	assert.Equal(t, payout, ramp.RailsOf(supports, cpf, "BRA"))
}

func TestBusinessKycOf(t *testing.T) {
	adapter := servesCompanies{}
	got, ok := ramp.BusinessKycOf(adapter)
	assert.True(t, ok)
	assert.Equal(t, []string{ramp.CompanyDocumentFormation}, got.BusinessApplicationDocuments("BRA", true))

	_, ok = ramp.BusinessKycOf(withPolicy{})
	assert.False(t, ok)
}

// The company document purposes are XIP's (spec §18.4): each adapter
// translates them into its provider's words.
func TestCompanyDocumentPurposes(t *testing.T) {
	assert.Equal(t, []string{"formation", "proof_of_address", "source_of_funds", "tax_identification", "nature_of_business"},
		ramp.CompanyDocumentPurposes())
}
