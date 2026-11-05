package kyc

import (
	"regexp"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	stepPersonal       = "personal"
	stepProfile        = "profile"
	stepAddress        = "address"
	stepProofOfAddress = "proof_of_address"
	stepBankStatement  = "bank_statement"
	stepDocument       = "document"
	stepSelfie         = "selfie"
)

var (
	whitespacePattern = regexp.MustCompile(`\s+`)
	dashPattern       = regexp.MustCompile(`[-–—]+`)
)

var pendingStepOrder = []string{stepPersonal, stepProfile, stepAddress, stepProofOfAddress, stepBankStatement, stepDocument, stepSelfie}

// missingCodeSteps holds two vocabularies at once, and that is deliberate.
//
// Bridge names the problem with a field; Ozav names the field itself
// ("residential_address"), because its missing_fields enum is a list of paths
// on the customer object. Neither provider is going to adopt the other's
// words, so the mapping to a wizard step is where they meet — and it has to
// be one mapping, in one place, or the client ends up holding a copy that
// speaks only whichever provider it was written for. `proof_of_address` is
// the one code both providers agree on, and at both it names a document —
// Bridge's `documents[].purposes` and Ozav's `still_required` — not a typed
// field, so it opens the upload step rather than the address step.
var missingCodeSteps = map[string]string{
	"tax_id":             stepPersonal,
	"tax_identification": stepPersonal,
	"tax_identification_number_not_compatible": stepPersonal,

	"mother_name":   stepPersonal,
	"date_of_birth": stepPersonal,
	"legal_name":    stepPersonal,
	"nationality":   stepPersonal,

	// Ozav's risk-profile paths. They are not the gate — measured 03/09,
	// filling them clears missing_fields without moving the customer — but the
	// app collects them now, so a provider naming one gets the step that
	// collects it rather than silence.
	"occupation":                  stepProfile,
	"source_of_funds":             stepProfile,
	"monthly_income_usd":          stepProfile,
	"expected_monthly_volume_usd": stepProfile,
	"is_pep":                      stepProfile,
	"account_purpose":             stepProfile,

	// Bridge, for a resident of the United States (missing-requirements docs,
	// KYC USA spec §2.7), and the source of funds questionnaire it asks a
	// high-risk customer — 60 or older among them — which source_of_funds
	// clears (sandbox, 14/10/2026).
	"tax_identification_number":                       stepPersonal,
	"tax_identification_number_missing":               stepPersonal,
	"database_check_failed_on_social_security_number": stepPersonal,
	"min_age_18":                                 stepPersonal,
	"source_of_funds_questionnaire":              stepProfile,
	"address_of_residence":                       stepAddress,
	"residence_address_invalid_subdivision_code": stepAddress,
	"subdivision_not_ny_usa":                     stepAddress,
	"subdivision_not_ak_usa":                     stepAddress,
	"government_id_document_missing":             stepDocument,
	"government_id_front_image_missing":          stepDocument,
	"selfie_missing":                             stepSelfie,
	"po_box":                                     stepAddress,
	"address":                                    stepAddress,

	"residential_address": stepAddress,

	"proof_of_address": stepProofOfAddress,

	// Ozav's still_required and documents_due name it for every individual:
	// its compliance measures the declared income against a statement. Like
	// the proof of address it is a file, so it opens an upload step.
	"bank_statement": stepBankStatement,

	// Ozav's own CustomerDocumentType vocabulary, from the `still_required`
	// list on GET /customers/{id}/documents. It shares no word with Bridge's,
	// and unmapped it produced no step at all — which the app answers by
	// running its whole wizard again for someone who is missing a photograph.
	"identity_document": stepDocument,

	"government_id":           stepDocument,
	"government_id_document":  stepDocument,
	"national_id":             stepDocument,
	"driver_license":          stepDocument,
	"identifying_information": stepDocument,
	"id_expired":              stepDocument,

	"live_selfie":     stepSelfie,
	"selfie":          stepSelfie,
	"selfie_document": stepSelfie,
}

// Codes that may appear in missing[] but must not open a wizard step.
//
// Because the fan-out's redistribution filter asks this same question, a code
// left here also makes every resubmission re-send documents to a provider
// that had already approved.
var nonUIMissingCodes = map[string]struct{}{
	"external_account":     {},
	"account_processing":   {},
	"account_provisioning": {},
	"post_processing":      {},

	// phone and email are here, not under personal, on measured evidence: Ozav
	// lists both on customers it has fully approved. The app always sends both,
	// so a provider still naming them is not asking the user for something they
	// withheld — reopening the personal step would show them the values they
	// already gave and change nothing.
	"phone": {},
	"email": {},
	// Bridge's PIX spelling of the same phone.
	"phone_number_missing": {},
}

var documentRejectionPhrases = []string{
	"cannot validate id",
	"same side submitted",
	"no government id",
	"incorrect id type",
	"id is expired",
	"cannot accept provided id",
	"document unreadable",
	"invalid document",
}

// normalizeMissingText lowercases, trims, collapses whitespace, and normalizes dashes.
func normalizeMissingText(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ToLower(s)
	s = whitespacePattern.ReplaceAllString(s, " ")
	s = dashPattern.ReplaceAllString(s, "-")
	return s
}

// normalizeMissingCode prepares Bridge missing codes for exact lookup.
func normalizeMissingCode(raw string) string {
	s := normalizeMissingText(raw)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

// PendingStepsFromMissing maps Bridge missing[] codes to stable wizard step ids.
func PendingStepsFromMissing(missing []string) []string {
	selected := map[string]struct{}{}

	for _, raw := range missing {
		code := normalizeMissingCode(raw)
		if code == "" {
			continue
		}
		if _, skip := nonUIMissingCodes[code]; skip {
			continue
		}
		// A rejection category, which OutstandingFor lists with the rest: the
		// steps it reopens, several at once, or none for a terminal one.
		if steps := models.KycRejectionSteps(code); len(steps) > 0 {
			for _, step := range steps {
				selected[step] = struct{}{}
			}
			continue
		}
		if step, ok := missingCodeSteps[code]; ok {
			selected[step] = struct{}{}
			continue
		}
		if documentRejectionPhrase(normalizeMissingText(raw)) {
			selected[stepDocument] = struct{}{}
		}
	}

	return sortedPendingSteps(selected)
}

func documentRejectionPhrase(norm string) bool {
	for _, phrase := range documentRejectionPhrases {
		if strings.Contains(norm, phrase) {
			return true
		}
	}
	return false
}

// sortedPendingSteps is selected in wizard order: a person's steps, or a
// company's (models.KybSteps) — one customer is never both.
func sortedPendingSteps(selected map[string]struct{}) []string {
	if len(selected) == 0 {
		return nil
	}
	out := make([]string, 0, len(selected))
	for _, step := range slices.Concat(pendingStepOrder, models.KybSteps) {
		if _, ok := selected[step]; ok {
			out = append(out, step)
		}
	}
	return out
}

// documentSatisfiableSteps are the steps a document call can actually answer.
// personal, profile and address are collected through the application
// endpoint, not the documents one — a provider naming only one of those is
// naming something real, but not something a document resubmission touches.
// A business's documents call answers its people's IDs and its own
// documents.
var documentSatisfiableSteps = map[string]struct{}{
	stepProofOfAddress: {},
	stepBankStatement:  {},
	stepDocument:       {},
	stepSelfie:         {},

	models.KybStepOwners:           {},
	models.KybStepCompanyDocuments: {},
}

// DocumentSteps narrows a pending-step list to the ones a document
// resubmission can satisfy, in pendingStepOrder order. The redistribution
// filter reads it: see ShouldSkipRedistribution for the customers it keeps
// from receiving their documents again.
func DocumentSteps(steps []string) []string {
	selected := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if _, ok := documentSatisfiableSteps[step]; ok {
			selected[step] = struct{}{}
		}
	}
	return sortedPendingSteps(selected)
}
