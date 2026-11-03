package kyc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// What Bridge's KYB names a business is missing opens the PJ step that
// collects it, in wizard order (API contract §7.1): a partner's ID is
// owners', a company document company_documents'; its own review and post
// processing open nothing (sandbox 18/10/2026).
func TestBusinessPendingStepsFromMissing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing []string
		want    []string
	}{
		{"right after the create", []string{"kyb_review", "government_id_document", "business_formation_document", "post_processing"},
			[]string{models.KybStepOwners, models.KybStepCompanyDocuments}},
		{"one company document again", []string{"proof_of_address_document"}, []string{models.KybStepCompanyDocuments}},
		{"the company's data", []string{"business_name", "address_of_incorporation", "minimal_source_of_funds_data"},
			[]string{models.KybStepCompany, models.KybStepCompanyAddress, models.KybStepActivity}},
		{"a correctable refusal", []string{models.KycRejectionDataMismatch}, []string{models.KybStepCompany, models.KybStepOwners}},
		{"a terminal refusal", []string{models.KycRejectionNotEligible}, nil},
		{"nothing the user can send", []string{"kyb_review", "external_account"}, nil},
		{"Bridge waiting on the owners", []string{"awaiting_ubo"}, []string{models.KybStepOwners}},
		{"Bridge waiting on the questionnaire", []string{"awaiting_questionnaire"}, []string{models.KybStepActivity}},
		// Bridge's either-or for a company with no website (sandbox
		// 19/10/2026): a website on the company step, or the document.
		{"a company without a website", []string{"business_website", "proof_of_nature_of_business_document",
			"proof_of_nature_of_business_document_verification"}, []string{models.KybStepCompany, models.KybStepCompanyDocuments}},
		// A document under verification is Bridge's to finish, as a
		// person's government_id_verification is: nothing to send.
		{"a document being verified", []string{"proof_of_nature_of_business_document_verification"}, nil},
		// The owners list is answered by the representative's attestation,
		// which the owners step collects, before any document.
		{"the ownership unconfirmed", []string{"business_ownership_document", "ownership_structure_attestation"},
			[]string{models.KybStepOwners}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, BusinessPendingStepsFromMissing(tc.missing))
		})
	}
}

// pending_documents names the company documents the provider still asks
// for, in the XIP vocabulary and the order the app asks them (contract §7.2),
// under either of Bridge's spellings.
func TestPendingDocumentsFromMissing(t *testing.T) {
	assert.Equal(t, []string{"formation", "proof_of_address", "source_of_funds", "tax_identification"},
		PendingDocumentsFromMissing([]string{"proof_of_tax_identification", "proof_of_source_of_funds_document",
			"government_id_document", "proof_of_address_document", "business_formation_document"}))
	assert.Equal(t, []string{"source_of_funds"}, PendingDocumentsFromMissing([]string{"proof_of_source_of_funds"}))
	assert.Empty(t, PendingDocumentsFromMissing([]string{"kyb_review", "government_id_document"}))
}

// Every company document Bridge's KYB names missing (missing requirements
// guide; sandbox 18–19/10/2026) opens company_documents and names its
// purpose, so the "one document missing" screen is never left without one
// while Bridge waits on a document of the company. A partner's ID is the
// owners step's, and the ownership document is answered by the owners
// step's attestation: neither names a company document.
func TestEveryCompanyDocumentBridgeNamesIsOneTheAppCanSend(t *testing.T) {
	for code, purpose := range map[string]string{
		"business_formation_document":                     "formation",
		"proof_of_address_document":                       "proof_of_address",
		"proof_of_source_of_funds_document":               "source_of_funds",
		"proof_of_tax_identification_document":            "tax_identification",
		"proof_of_nature_of_business_document":            "nature_of_business",
		"proof_of_nature_of_business_document_in_persona": "nature_of_business",
	} {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, []string{models.KybStepCompanyDocuments}, BusinessPendingStepsFromMissing([]string{code}))
			assert.Equal(t, []string{purpose}, PendingDocumentsFromMissing([]string{code}))
			assert.Contains(t, ramp.CompanyDocumentPurposes(), purpose, "a purpose the documents call takes")
		})
	}
	for code, step := range map[string]string{
		"government_id_document":      models.KybStepOwners,
		"business_ownership_document": models.KybStepOwners,
	} {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, []string{step}, BusinessPendingStepsFromMissing([]string{code}))
			assert.Empty(t, PendingDocumentsFromMissing([]string{code}))
		})
	}
}

func businessRow(status string, requirements ...string) models.CustomerProviderKyc {
	row := models.CustomerProviderKyc{
		Provider: models.ProviderBridge, KycStatus: status, TosStatus: models.TosStatusApproved, Business: true,
		Endorsements: "[]", Requirements: "[]", RejectionReasons: "[]", RejectionCategories: "[]", RawStatus: "{}",
	}
	row.SetRequirements(requirements)
	return row
}

// A company that never applied owes the whole KYB, terms included while
// they are pending (API contract §7.1): the six PJ steps, never a person's.
func TestPendingStepsFor_ABusinessOwingItsApplication(t *testing.T) {
	row := businessRow(models.KycStatusNotStarted)
	row.NeedsApplication, row.TosStatus = true, models.TosStatusPending

	assert.Equal(t, models.KybSteps, PendingStepsFor(&row))

	row.TosStatus = models.TosStatusApproved
	assert.Equal(t, []string{models.KybStepCompany, models.KybStepCompanyAddress, models.KybStepActivity,
		models.KybStepOwners, models.KybStepCompanyDocuments}, PendingStepsFor(&row))
}

// After the application, what the provider names opens its step; an e-mail
// the provider holds for another customer reopens the company.
func TestPendingStepsFor_ABusinessAfterItsApplication(t *testing.T) {
	row := businessRow(models.KycStatusRequiresInfo, "kyb_review", "government_id_document", "proof_of_address_document")
	assert.Equal(t, []string{models.KybStepOwners, models.KybStepCompanyDocuments}, PendingStepsFor(&row))

	failed := businessRow(models.KycStatusNotStarted)
	failed.RecordSubmissionFailure("email_in_use", "bridge: duplicate e-mail")
	assert.Equal(t, []string{models.KybStepCompany}, PendingStepsFor(&failed))
}

// The overview of a business reads its rows the company's way: a correctable
// refusal opens its PJ steps, and the documents the provider named are listed.
func TestComputeOverall_ABusiness(t *testing.T) {
	rejected := businessRow(models.KycStatusRejected)
	rejected.SetRejectionCategories([]string{models.KycRejectionDocumentUnreadable})
	overall := ComputeOverall([]models.CustomerProviderKyc{rejected}, nil, true, time.Now())
	assert.Equal(t, OverallActionRequired, overall.Status)
	assert.Equal(t, []string{models.KybStepOwners, models.KybStepCompanyDocuments}, overall.PendingSteps)

	owing := businessRow(models.KycStatusIncomplete, "proof_of_address_document")
	overall = ComputeOverall([]models.CustomerProviderKyc{owing}, nil, true, time.Now())
	assert.Equal(t, OverallActionRequired, overall.Status)
	assert.Equal(t, []string{models.KybStepCompanyDocuments}, overall.PendingSteps)
	assert.Equal(t, []string{"proof_of_address"}, overall.PendingDocuments)

	fresh := businessRow(models.KycStatusNotStarted)
	fresh.NeedsApplication, fresh.TosStatus = true, models.TosStatusPending
	overall = ComputeOverall([]models.CustomerProviderKyc{fresh}, nil, false, time.Now())
	assert.Equal(t, OverallNotStarted, overall.Status)
	assert.Equal(t, models.KybSteps, overall.PendingSteps)
	assert.Empty(t, overall.PendingDocuments)
}

// The documents fan-out passes over a business's provider on the company's
// steps, not a person's: Bridge's KYB codes name nothing a person's map
// knows, and read that way every retry of §6.2 was dropped as nothing owed.
func TestShouldSkipBusinessRedistribution(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       string
		requirements []string
		skip         bool
	}{
		{"a company document again", models.KycStatusRequiresInfo, []string{"business_formation"}, false},
		{"a partner's ID", models.KycStatusIncomplete, []string{"government_id_document"}, false},
		{"Bridge waiting on the owners", models.KycStatusRequiresInfo, []string{"awaiting_ubo"}, false},
		{"named nothing yet", models.KycStatusIncomplete, nil, false},
		{"named only what no document answers", models.KycStatusRequiresInfo, []string{"kyb_review"}, true},
		{"approved, owing only the company's data", models.KycStatusApproved, []string{"business_name"}, true},
		{"approved, owing a company document", models.KycStatusApproved, []string{"proof_of_address"}, false},
		{"first submission", models.KycStatusNotStarted, nil, false},
		{"in review", models.KycStatusUnderReview, nil, true},
		{"approved", models.KycStatusApproved, nil, true},
		{"refused over an unreadable document", models.KycStatusRejected, []string{models.KycRejectionDocumentUnreadable}, false},
		{"refused over the company's data", models.KycStatusRejected, []string{models.KycRejectionDataMismatch}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.skip, ShouldSkipBusinessRedistribution(tc.status, tc.requirements))
		})
	}
}

// What an application carries for a business: the company, its address, its
// activity and its people — not its documents, not the terms.
func TestOwesApplicationDataOfABusiness(t *testing.T) {
	for _, step := range []string{models.KybStepCompany, models.KybStepCompanyAddress, models.KybStepActivity, models.KybStepOwners} {
		assert.True(t, owesApplicationData([]string{step}), step)
	}
	assert.False(t, owesApplicationData([]string{models.KybStepCompanyDocuments, models.KybStepTerms}))
}
