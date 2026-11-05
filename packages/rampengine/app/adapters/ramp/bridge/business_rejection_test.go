package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// A refused business is mapped by the issues Bridge's KYB raises (spec §7.4),
// with the person codes of each partner mapped as the PF ones are. Without
// this table every refusal of a company would be not_eligible, which is
// terminal: no fix would ever reach Bridge again.
func TestBusinessRejection(t *testing.T) {
	rejected := func(issues ...string) customerResponse {
		return customerResponse{Type: "business", Status: "rejected", Endorsements: []endorsementResponse{
			{Name: "pix_onramp", Requirements: endorsementRequirements{Issues: issues}},
		}}
	}
	cases := map[string][]string{
		"rejected_due_to_inaccurate_onboarding_details":            {models.KycRejectionDataMismatch},
		"business_registry_no_match":                               {models.KycRejectionDataMismatch},
		"business_ein_no_match":                                    {models.KycRejectionDataMismatch},
		"business_website_failure":                                 {models.KycRejectionDataMismatch},
		"sole_proprietorship_too_many_ubos":                        {models.KycRejectionDataMismatch},
		"business_formation_document_verification_failed":          {models.KycRejectionDocumentUnreadable},
		"business_ownership_document_verification_failed":          {models.KycRejectionDocumentUnreadable},
		"proof_of_nature_of_business_document_verification_failed": {models.KycRejectionDocumentUnreadable},
		"phone_number.duplicate_customer_detected":                 {models.KycRejectionPhoneInUse},
		"government_id_verification_failed":                        {models.KycRejectionDocumentUnreadable},
		"rejected_due_to_unsupported_geo":                          {models.KycRejectionNotEligible},
		"rejected_due_to_unsupported_business_activities":          {models.KycRejectionNotEligible},
		"rejected_due_to_unidentifiable_business_entity":           {models.KycRejectionNotEligible},
		"high_risk_business_activities":                            {models.KycRejectionNotEligible},
		"operates_in_prohibited_countries":                         {models.KycRejectionNotEligible},
		"manual_review_rejected":                                   {models.KycRejectionNotEligible},
	}
	for issue, want := range cases {
		t.Run(issue, func(t *testing.T) {
			mapping := rejected(issue).rejection()
			require.Equal(t, want, mapping.categories)
			require.Empty(t, mapping.unmapped, "a mapped issue asks nobody to map it")
		})
	}

	// A correctable company issue beside a terminal one is terminal.
	require.Equal(t, []string{models.KycRejectionDataMismatch, models.KycRejectionNotEligible},
		rejected("business_registry_no_match", "manual_review_rejected").rejection().categories)
	// A code Bridge invents later is still not_eligible, and still logged.
	unknown := rejected("business_something_new").rejection()
	require.Equal(t, []string{models.KycRejectionNotEligible}, unknown.categories)
	require.Equal(t, []string{"business_something_new"}, unknown.unmapped)
}

// The KYB's codes are a business's: a person refused with one keeps the
// category a person always got for it, not_eligible, as an unmapped code
// (they were never seen on a person).
func TestAPersonsRejectionDoesNotReadBusinessCodes(t *testing.T) {
	for _, issue := range []string{"rejected_due_to_inaccurate_onboarding_details", "business_registry_no_match", "manual_review_rejected"} {
		person := customerResponse{Type: "individual", Status: "rejected", Endorsements: []endorsementResponse{
			{Name: "pix_onramp", Requirements: endorsementRequirements{Issues: issueCodes{issue}}},
		}}
		mapping := person.rejection()
		require.Equal(t, []string{models.KycRejectionNotEligible}, mapping.categories, issue)
		require.Equal(t, []string{issue}, mapping.unmapped, issue)
	}
}
