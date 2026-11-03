package ozav

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestKycStatusFromOzav(t *testing.T) {
	cases := []struct{ ozavStatus, want string }{
		// A customer Ozav holds is not one nobody started. `pending` is what
		// it reports for a record that exists and owes requirements — its own
		// panel badges that customer "In review" — and not_started means the
		// opposite in XIP's vocabulary: this user never touched the provider.
		// Reading it as not_started is what told a real user, holding a
		// customer under review, that they had been refused and should begin
		// again (02/09).
		{"pending", models.KycStatusIncomplete},
		{"in_review", models.KycStatusUnderReview},
		{"approved", models.KycStatusApproved},
		{"rejected", models.KycStatusRejected},
		{"suspended", models.KycStatusPaused},
		// Anything we do not recognise must not grant and must not send the
		// user chasing requirements we cannot name.
		{"some_new_state", models.KycStatusUnderReview},
		{"", models.KycStatusUnderReview},
	}
	for _, c := range cases {
		require.Equal(t, c.want, kycStatusFromOzav(c.ozavStatus), "ozav status %q", c.ozavStatus)
	}
}

func TestCustomerKycStatusDowngradesApprovedWithoutVerificationMode(t *testing.T) {
	cases := []struct {
		name            string
		status, verMode string
		want            string
	}{
		{"approved with a verification mode on record is approved", "approved", "full", models.KycStatusApproved},
		{"approved with no verification mode on record is under_review, not approved",
			"approved", "", models.KycStatusUnderReview},
		{"a non-approved status is unaffected by mode", "pending", "", models.KycStatusIncomplete},
		{"rejected is unaffected by mode even when a mode is present", "rejected", "full", models.KycStatusRejected},
		{"unknown status is unaffected by mode", "some_new_state", "full", models.KycStatusUnderReview},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, customerKycStatus(c.status, c.verMode))
		})
	}
}

// Ozav keeps a customer `in_review` while it waits on data: both real customers
// its compliance chased on 28/09/2026 read `in_review`, one with seven
// missing_fields (owner still `ozav`: a human is on the case) and one with a
// bank statement in documents_due. under_review told the app there was nothing
// to do, so it showed a "we are reviewing" screen for a review that could not
// move. The user has something to send, which is what incomplete means.
func TestCustomerStatusIsIncompleteWhileOzavWaitsOnData(t *testing.T) {
	due := func(status string) customerRequirements {
		return customerRequirements{DocumentsDue: []documentDue{{DocType: "bank_statement", Status: status}}}
	}
	cases := []struct {
		name         string
		status       string
		requirements customerRequirements
		want         string
	}{
		{"in review with missing fields", "in_review",
			customerRequirements{MissingFields: []string{"occupation"}, Owner: "ozav"}, models.KycStatusIncomplete},
		{"in review with a document due", "in_review", due("due"), models.KycStatusIncomplete},
		{"in review with a document rejected", "in_review", due("rejected"), models.KycStatusIncomplete},
		{"in review with a document already submitted", "in_review", due("submitted"), models.KycStatusUnderReview},
		{"in review owing nothing", "in_review", customerRequirements{Owner: "ozav"}, models.KycStatusUnderReview},
		// Approved customers carry risk-profile missing_fields as a matter of
		// course (measured 03/09); they do not take an approval away.
		{"approved with missing fields", "approved",
			customerRequirements{MissingFields: []string{"is_pep"}}, models.KycStatusApproved},
		{"rejected with a document due", "rejected", due("due"), models.KycStatusRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			customer := customerResponse{Status: c.status, Verification: verificationResponse{Mode: "reliance"},
				Requirements: c.requirements}
			require.Equal(t, c.want, customerStatus(customer))
		})
	}
}

func TestTransferStatusFromOzav(t *testing.T) {
	cases := []struct{ ozavStatus, want string }{
		{"awaiting_payment", models.TxStatusAwaitingFunds},
		{"payment_confirmed", models.TxStatusFundsReceived},
		{"converting", models.TxStatusProcessing},
		{"payout_initiated", models.TxStatusProcessing},
		{"completed", models.TxStatusCompleted},
		{"failed", models.TxStatusFailed},
		{"cancelled", models.TxStatusFailed},
		{"expired", models.TxStatusExpired},
		{"needs_review", models.TxStatusProcessing},
		// An unknown state must not be terminal — that would freeze the
		// order — and must never be completed, which would credit one that
		// is not.
		{"some_new_state", models.TxStatusProcessing},
		{"", models.TxStatusProcessing},
	}
	for _, c := range cases {
		require.Equal(t, c.want, transferStatusFromOzav(c.ozavStatus), "ozav transfer status %q", c.ozavStatus)
	}
}

// Ozav keeps a customer `pending` both while it reviews and while it waits on
// the integrator; decision.status tells them apart. Read as incomplete, a
// pending_review customer opened the app's wizard with no step in it.
func TestCustomerStatusReadsTheDecisionOfAPendingCustomer(t *testing.T) {
	decided := func(status string) *customerDecision { return &customerDecision{Status: status} }
	cases := []struct {
		name         string
		decision     *customerDecision
		requirements customerRequirements
		want         string
	}{
		{"pending review owing nothing", decided("pending_review"), customerRequirements{Owner: "ozav"},
			models.KycStatusUnderReview},
		{"pending review with a field missing", decided("pending_review"),
			customerRequirements{MissingFields: []string{"tax_id"}, Owner: "integrator"}, models.KycStatusIncomplete},
		{"information requested", decided("information_requested"),
			customerRequirements{Owner: "integrator"}, models.KycStatusIncomplete},
		{"no decision on record", nil, customerRequirements{}, models.KycStatusIncomplete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			customer := customerResponse{Status: "pending", Verification: verificationResponse{Mode: "reliance"},
				Decision: c.decision, Requirements: c.requirements}
			require.Equal(t, c.want, customerStatus(customer))
		})
	}
}

// The customer of 29/10/2026: information_requested for a document_missing,
// owner integrator, every documents_due line submitted or accepted, and
// still_required empty. Nothing names what to send, which is what the
// operator has to be told about; anything naming a file, a field or a due
// item is an ask a step can answer.
func TestUnnamedRequestIsAnAskThatNamesNothing(t *testing.T) {
	lines := []documentDue{
		{DocType: "bank_statement", Status: "accepted"},
		{DocType: "identity_document", Status: "submitted"},
		{DocType: "proof_of_address", Status: "accepted"},
	}
	asking := customerResponse{Status: "pending",
		Decision:     &customerDecision{Status: "information_requested", ReasonCode: "document_missing"},
		Requirements: customerRequirements{Owner: "integrator", DocumentsDue: lines}}
	require.True(t, asking.unnamedRequest(nil))
	require.Equal(t, models.KycStatusIncomplete, customerStatus(asking))

	require.False(t, asking.unnamedRequest([]string{"identity_document"}), "still_required names it")

	owing := asking
	owing.Requirements.DocumentsDue = append([]documentDue{{DocType: "identity_document", Status: "due"}}, lines...)
	require.False(t, owing.unnamedRequest(nil), "a due line names it")

	reviewing := customerResponse{Status: "pending", Decision: &customerDecision{Status: "pending_review"},
		Requirements: customerRequirements{Owner: "ozav", DocumentsDue: lines}}
	require.False(t, reviewing.unnamedRequest(nil), "Ozav's move, not an ask")

	rejected := asking
	rejected.Status = "rejected"
	require.False(t, rejected.unnamedRequest(nil), "a rejection is final, not an ask")
}

// The customer of 29/10/2026: Ozav waits on the integrator with the identity
// document submitted but marked document_invalid. Only a new file moves it, so
// it is owed again; while Ozav itself reviews, the same line is not.
func TestAnInvalidDocumentIsOwedAgainWhileOzavWaitsOnTheIntegrator(t *testing.T) {
	lines := []documentDue{
		{DocType: "bank_statement", Status: "accepted"},
		{DocType: "identity_document", Status: "submitted", ReasonCode: "document_invalid"},
		{DocType: "proof_of_address", Status: "accepted"},
	}
	asking := customerResponse{Status: "pending",
		Decision:     &customerDecision{Status: "information_requested", ReasonCode: "document_missing"},
		Requirements: customerRequirements{Owner: "integrator", DocumentsDue: lines}}

	require.Equal(t, []string{"identity_document"}, asking.Requirements.owedDocuments())
	require.Equal(t, models.KycStatusIncomplete, customerStatus(asking))
	require.False(t, asking.unnamedRequest(nil), "the invalid line names what to send")

	reviewing := customerResponse{Status: "pending", Decision: &customerDecision{Status: "pending_review"},
		Requirements: customerRequirements{Owner: "ozav", DocumentsDue: lines}}
	require.Empty(t, reviewing.Requirements.owedDocuments())
	require.Equal(t, models.KycStatusUnderReview, customerStatus(reviewing))
}
