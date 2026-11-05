package bridge

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// Bridge wants the formation document of every company, and the proof of
// source of funds and of address of every one asking for Pix, which is every
// company it serves (BRL ramp migration guide, 15/06/2026); a Mexican one
// also proves its tax registration (businesses/formation guide).
func TestBusinessApplicationDocuments(t *testing.T) {
	base := []string{ramp.CompanyDocumentFormation, ramp.CompanyDocumentProofOfAddress, ramp.CompanyDocumentSourceOfFunds}
	adapter := &Adapter{}

	assert.Equal(t, base, adapter.BusinessApplicationDocuments("BRA", true))
	assert.Equal(t, base, adapter.BusinessApplicationDocuments("USA", true))
	assert.Equal(t, append(slices.Clone(base), ramp.CompanyDocumentTaxIdentification), adapter.BusinessApplicationDocuments("mx", true))
}

// A company with no website proves what it does instead: Bridge asks
// business_website or proof_of_nature_of_business_document, and a company
// that declared none answers the second (sandbox 19/10/2026).
func TestBusinessApplicationDocumentsOfACompanyWithoutAWebsite(t *testing.T) {
	adapter := &Adapter{}

	assert.Equal(t, []string{ramp.CompanyDocumentFormation, ramp.CompanyDocumentProofOfAddress, ramp.CompanyDocumentSourceOfFunds,
		ramp.CompanyDocumentNatureOfBusiness}, adapter.BusinessApplicationDocuments("BRA", false))
	assert.Equal(t, []string{ramp.CompanyDocumentFormation, ramp.CompanyDocumentProofOfAddress, ramp.CompanyDocumentSourceOfFunds,
		ramp.CompanyDocumentTaxIdentification, ramp.CompanyDocumentNatureOfBusiness}, adapter.BusinessApplicationDocuments("MEX", false))
}

// Through the jurisdiction policy the stack reads, a company reaches the rails
// Bridge's business branch opens.
func TestRailsOfServesACompanyThroughBridge(t *testing.T) {
	cnpj := ramp.TaxIdentity{Type: "cnpj", Country: "BRA"}
	assert.Equal(t, ramp.PixRails(), ramp.RailsOf(&Adapter{}, cnpj, "BRA"))
}
