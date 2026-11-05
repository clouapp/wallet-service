package models

// Rejection categories: why a provider refused the user, in XIP's vocabulary.
// Each adapter translates its provider's own reasons onto these, the way it
// translates its statuses onto the KycStatus constants; the provider's own
// words stay in `rejection_details`, for the admin only. The app owns the
// text shown for each.
//
// Two of them are terminal: no resubmission fixes a tax id another person
// already holds, or a refusal we cannot name (not_eligible covers sanctions,
// PEP, blocklist, fraud, age and every code nobody has mapped yet). Any
// terminal category makes the provider terminal for that user — it never
// receives their data again.
const (
	KycRejectionSelfieMismatch     = "selfie_mismatch"
	KycRejectionDocumentUnreadable = "document_unreadable"
	// A supporting document the provider asked for again because the file
	// sent was not the one requested (Ozav's documents_due, document_invalid).
	// The provider has not refused the user: it is still waiting for a better
	// file, so the row stays incomplete and carries the category.
	KycRejectionProofOfAddressInvalid = "proof_of_address_invalid"
	KycRejectionBankStatementInvalid  = "bank_statement_invalid"
	KycRejectionDataMismatch          = "data_mismatch"
	KycRejectionPhoneInUse            = "phone_in_use"
	KycRejectionTaxIDInUse            = "tax_id_in_use"
	KycRejectionNotEligible           = "not_eligible"
)

// KycRejectionCategories lists every category once, in the order a list of
// them is presented.
var KycRejectionCategories = []string{
	KycRejectionSelfieMismatch, KycRejectionDocumentUnreadable,
	KycRejectionProofOfAddressInvalid, KycRejectionBankStatementInvalid, KycRejectionDataMismatch,
	KycRejectionPhoneInUse, KycRejectionTaxIDInUse, KycRejectionNotEligible,
}

// kycRejectionSteps is the wizard steps each corrigible category reopens, in
// the wizard's own step ids. A terminal category has no entry: there is
// nothing for the user to redo.
var kycRejectionSteps = map[string][]string{
	KycRejectionSelfieMismatch:     {"document", "selfie"},
	KycRejectionDocumentUnreadable: {"document"},
	KycRejectionDataMismatch:       {"personal", "document"},
	KycRejectionPhoneInUse:         {"personal"},
	// A person's supporting documents; a business has no such step.
	KycRejectionProofOfAddressInvalid: {"proof_of_address"},
	KycRejectionBankStatementInvalid:  {"bank_statement"},
}

var kycRejectionTerminal = map[string]bool{
	KycRejectionTaxIDInUse:  true,
	KycRejectionNotEligible: true,
}

// The steps of a business's KYB wizard (KYB spec §7, API contract §7.1), in
// its order. A person's steps never appear in a business's scope, nor these
// in a person's.
const (
	KybStepCompany          = "company"
	KybStepCompanyAddress   = "company_address"
	KybStepActivity         = "activity"
	KybStepOwners           = "owners"
	KybStepCompanyDocuments = "company_documents"
	KybStepTerms            = "terms"
)

// KybSteps lists them in wizard order.
var KybSteps = []string{
	KybStepCompany, KybStepCompanyAddress, KybStepActivity, KybStepOwners, KybStepCompanyDocuments, KybStepTerms,
}

// businessKycRejectionSteps is kycRejectionSteps for a business. The rows
// keep the category, not the issue, so a category reopens every step one of
// its issues may need: the company and the partners together, since a
// partner's own refusal (their data, their document) is the PF vocabulary
// the partner's codes map onto (API contract §7.3).
var businessKycRejectionSteps = map[string][]string{
	KycRejectionDataMismatch:       {KybStepCompany, KybStepOwners},
	KycRejectionDocumentUnreadable: {KybStepCompanyDocuments, KybStepOwners},
	KycRejectionPhoneInUse:         {KybStepCompany, KybStepOwners},
	// Not expected: Bridge asks no partner for a selfie.
	KycRejectionSelfieMismatch: {KybStepOwners},
}

// KycRejectionSteps returns the wizard steps category reopens for a person,
// or nil for a terminal or unknown one.
func KycRejectionSteps(category string) []string {
	return KycRejectionStepsFor(CustomerTypeIndividual, category)
}

// KycRejectionStepsFor returns the wizard steps category reopens in the
// wizard of a customer of customerType, or nil for a terminal or unknown one.
func KycRejectionStepsFor(customerType, category string) []string {
	if customerType == CustomerTypeBusiness {
		return businessKycRejectionSteps[category]
	}
	return kycRejectionSteps[category]
}

// KycRejectionIsTerminal reports whether category ends the user's KYC at the
// provider that gave it.
func KycRejectionIsTerminal(category string) bool {
	return kycRejectionTerminal[category]
}
