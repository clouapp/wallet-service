package bridge

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// BusinessApplicationDocuments is what Bridge wants from a company
// incorporated in incorporation: its formation document always, and the
// proof of source of funds and of address that every company asking for Pix
// owes since 15/06/2026 (BRL ramp migration guide) — every company Bridge
// serves asks for Pix (businessRails). A Mexican company also proves its tax
// registration (businesses/formation guide). One with no website proves its
// nature of business instead: Bridge asks business_website or
// proof_of_nature_of_business_document, and holds a company that has neither
// incomplete (sandbox 19/10/2026).
func (a *Adapter) BusinessApplicationDocuments(incorporation string, hasWebsite bool) []string {
	documents := []string{
		ramp.CompanyDocumentFormation, ramp.CompanyDocumentProofOfAddress, ramp.CompanyDocumentSourceOfFunds,
	}
	if code, _ := iso3166.Alpha3(incorporation); code == "MEX" {
		documents = append(documents, ramp.CompanyDocumentTaxIdentification)
	}
	if !hasWebsite {
		documents = append(documents, ramp.CompanyDocumentNatureOfBusiness)
	}
	return documents
}

var _ ramp.BusinessKycAdapter = (*Adapter)(nil)
