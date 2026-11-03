package kyc

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// businessMissingCodeSteps is missingCodeSteps for a business: what Bridge's
// KYB names a company is missing — its own fields, its documents by purpose,
// and a partner's, which it reports nested under the person — and the PJ step
// that collects each (API contract §7.1, sandbox 18/10/2026). A code nobody
// mapped opens nothing: the app reads it as in review, and the provider's
// next answer names what it wants.
var businessMissingCodeSteps = map[string]string{
	"business_name":             models.KybStepCompany,
	"business_type":             models.KybStepCompany,
	"business_description":      models.KybStepCompany,
	"business_industry":         models.KybStepCompany,
	"business_website":          models.KybStepCompany,
	"email_address":             models.KybStepCompany,
	"phone_number":              models.KybStepCompany,
	"incorporation_date":        models.KybStepCompany,
	"tax_identification_number": models.KybStepCompany,

	"address_of_incorporation": models.KybStepCompanyAddress,
	"address_of_operation":     models.KybStepCompanyAddress,

	"minimal_source_of_funds_data":  models.KybStepActivity,
	"source_of_funds":               models.KybStepActivity,
	"account_purpose":               models.KybStepActivity,
	"conducts_money_services":       models.KybStepActivity,
	"estimated_annual_revenue_usd":  models.KybStepActivity,
	"expected_monthly_payments_usd": models.KybStepActivity,

	// A partner's: the photographed ID every one owes, and what Bridge asks
	// of a person.
	"government_id_document":               models.KybStepOwners,
	"government_id":                        models.KybStepOwners,
	"control_person_ownership_attestation": models.KybStepOwners,
	// Bridge confirms the owners by a control person's attestation or by an
	// ownership document (businesses/ownership guide); the owners step
	// collects the attestation, so neither asks the company for a file.
	"ownership_structure_attestation": models.KybStepOwners,
	"business_ownership_document":     models.KybStepOwners,
	"address_of_residence":            models.KybStepOwners,
	"date_of_birth":                   models.KybStepOwners,

	// The states Bridge names as what a business owes (bridge
	// customerResponse.requirements): its owners — a partner's photographed
	// ID, once the documents call has settled — and its questionnaire.
	"awaiting_ubo":           models.KybStepOwners,
	"awaiting_questionnaire": models.KybStepActivity,
}

// businessDocumentPurposes maps a company document Bridge names missing, under
// each of its spellings, to its purpose in the XIP vocabulary: every company
// document the missing requirements guide names but the ownership one
// (businessMissingCodeSteps), so company_documents never opens without
// naming what to send. A document's _verification is Bridge checking one it
// has, as a person's government_id_verification is, and opens nothing.
var businessDocumentPurposes = map[string]string{
	"business_formation":                   ramp.CompanyDocumentFormation,
	"business_formation_document":          ramp.CompanyDocumentFormation,
	"proof_of_address":                     ramp.CompanyDocumentProofOfAddress,
	"proof_of_address_document":            ramp.CompanyDocumentProofOfAddress,
	"proof_of_source_of_funds":             ramp.CompanyDocumentSourceOfFunds,
	"proof_of_source_of_funds_document":    ramp.CompanyDocumentSourceOfFunds,
	"proof_of_tax_identification":          ramp.CompanyDocumentTaxIdentification,
	"proof_of_tax_identification_document": ramp.CompanyDocumentTaxIdentification,

	// Asked of a company with no website, beside business_website: either
	// answers it (sandbox 19/10/2026).
	"proof_of_nature_of_business":                     ramp.CompanyDocumentNatureOfBusiness,
	"proof_of_nature_of_business_document":            ramp.CompanyDocumentNatureOfBusiness,
	"proof_of_nature_of_business_document_in_persona": ramp.CompanyDocumentNatureOfBusiness,
}

// BusinessPendingStepsFromMissing is PendingStepsFromMissing for a business:
// its missing codes and rejection categories as PJ steps, in wizard order
// (models.KybSteps). A company document opens company_documents; a
// category opens the steps KycRejectionStepsFor names for a business, none
// for a terminal one.
func BusinessPendingStepsFromMissing(missing []string) []string {
	selected := map[string]bool{}
	for _, raw := range missing {
		code := normalizeMissingCode(raw)
		if steps := models.KycRejectionStepsFor(models.CustomerTypeBusiness, code); len(steps) > 0 {
			for _, step := range steps {
				selected[step] = true
			}
			continue
		}
		if _, ok := businessDocumentPurposes[code]; ok {
			selected[models.KybStepCompanyDocuments] = true
			continue
		}
		if step, ok := businessMissingCodeSteps[code]; ok {
			selected[step] = true
		}
	}
	return kybOrdered(selected)
}

// PendingDocumentsFromMissing is the company documents a provider still asks
// for among missing, as XIP purposes in the order the app asks them
// (ramp.CompanyDocumentPurposes): what the "one document missing" screen
// names (API contract §7.2).
func PendingDocumentsFromMissing(missing []string) []string {
	wanted := map[string]bool{}
	for _, raw := range missing {
		if purpose, ok := businessDocumentPurposes[normalizeMissingCode(raw)]; ok {
			wanted[purpose] = true
		}
	}
	var out []string
	for _, purpose := range ramp.CompanyDocumentPurposes() {
		if wanted[purpose] {
			out = append(out, purpose)
		}
	}
	return out
}

// businessApplicationSteps are the PJ steps an application covers, its
// documents call included: everything but the terms.
var businessApplicationSteps = []string{
	models.KybStepCompany, models.KybStepCompanyAddress, models.KybStepActivity,
	models.KybStepOwners, models.KybStepCompanyDocuments,
}

// businessNamedSteps is namedSteps for a business's row: what the provider
// names, the whole application when it owes one, the terms while they are
// pending, and the company step after the provider refused its e-mail as
// another customer's (email_in_use, which reaches the row as the last
// submission's failure).
func businessNamedSteps(row *models.CustomerProviderKyc) []string {
	selected := map[string]bool{}
	for _, step := range BusinessPendingStepsFromMissing(OutstandingFor(row)) {
		selected[step] = true
	}
	if row.NeedsApplication {
		for _, step := range businessApplicationSteps {
			selected[step] = true
		}
	}
	if row.TosStatus == models.TosStatusPending {
		selected[models.KybStepTerms] = true
	}
	if row.LastErrorCode != nil && *row.LastErrorCode == ramp.ErrorCode(ramp.ErrEmailInUse) {
		selected[models.KybStepCompany] = true
	}
	return kybOrdered(selected)
}

// kybOrdered is selected in wizard order.
func kybOrdered(selected map[string]bool) []string {
	var out []string
	for _, step := range models.KybSteps {
		if selected[step] {
			out = append(out, step)
		}
	}
	return out
}
