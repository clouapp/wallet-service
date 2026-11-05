package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The fixtures in testdata/rejected_*.json are the customer.updated deliveries
// stored on the rejected rows in production on 28/09/2026, reduced to the
// fields the mapping reads and with every id replaced. Their developer_reasons
// are verbatim: they are what the mapping has to recognise.
func TestRejectionFromProductionDeliveries(t *testing.T) {
	cases := []struct {
		fixture    string
		categories []string
		details    int
	}{
		{"rejected_phone_duplicate.json",
			[]string{models.KycRejectionPhoneInUse}, 1},
		{"rejected_selfie_and_id_country.json",
			[]string{models.KycRejectionSelfieMismatch, models.KycRejectionDocumentUnreadable}, 2},
		{"rejected_selfie_phone_and_id_country.json",
			[]string{models.KycRejectionSelfieMismatch, models.KycRejectionDocumentUnreadable, models.KycRejectionPhoneInUse}, 3},
		// government_id_verification_failed alone cannot tell an unreadable
		// document from one whose data contradicts the account; the
		// developer_reason is what says which.
		{"rejected_id_data_mismatch.json",
			[]string{models.KycRejectionDataMismatch}, 1},
		{"rejected_tax_id_and_phone_duplicate.json",
			[]string{models.KycRejectionSelfieMismatch, models.KycRejectionDocumentUnreadable,
				models.KycRejectionPhoneInUse, models.KycRejectionTaxIDInUse}, 4},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			result, err := New(nil, nil).DecodeWebhook(context.Background(), readFixture(t, c.fixture))
			require.NoError(t, err)
			require.Equal(t, models.KycStatusRejected, result.Status)
			require.Equal(t, c.categories, result.RejectionCategories)
			require.Len(t, result.RejectionDetails, c.details)
		})
	}
}

// The details are Bridge's developer_reasons as sent, for the admin; the
// user-facing reasons stay where they were.
func TestRejectionKeepsTheDeveloperReasonsAsDetails(t *testing.T) {
	result, err := New(nil, nil).DecodeWebhook(context.Background(), readFixture(t, "rejected_id_data_mismatch.json"))
	require.NoError(t, err)
	require.Equal(t, []string{
		"The information on the customer's submitted ID does not match their submitted account information in our system.",
	}, result.RejectionDetails)
	require.Equal(t, []string{"Your information could not be verified"}, result.RejectionReasons)
}

func TestCustomerRejection(t *testing.T) {
	endorsement := func(issues ...string) endorsementResponse {
		return endorsementResponse{Name: "pix", Requirements: endorsementRequirements{Issues: issues}}
	}
	reason := func(developer string) rejectionReason {
		return rejectionReason{Reason: "Your information could not be verified", DeveloperReason: developer}
	}
	cases := []struct {
		name       string
		customer   customerResponse
		categories []string
		unmapped   bool
	}{
		{"a customer that is not rejected has no category",
			customerResponse{Status: "incomplete", Endorsements: []endorsementResponse{endorsement("selfie_verification_failed")}},
			nil, false},
		{"issues from every endorsement are read",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("selfie_verification_failed"), endorsement("phone_number.duplicate_customer_detected"),
			}},
			[]string{models.KycRejectionSelfieMismatch, models.KycRejectionPhoneInUse}, false},
		{"an expired id is a document to send again",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{endorsement("id_expired")}},
			[]string{models.KycRejectionDocumentUnreadable}, false},
		{"a government id failure no reason explains is an unreadable document",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{endorsement("government_id_verification_failed")}},
			[]string{models.KycRejectionDocumentUnreadable}, false},
		// Issues first: a developer_reason nobody mapped does not turn an
		// issue that was mapped into a terminal refusal.
		{"an unmapped developer reason beside a mapped issue is ignored",
			customerResponse{Status: "rejected",
				Endorsements:     []endorsementResponse{endorsement("selfie_verification_failed")},
				RejectionReasons: []rejectionReason{reason("Something Bridge never said before.")}},
			[]string{models.KycRejectionSelfieMismatch}, false},
		// A US resident's SSN that failed the database check, or that Bridge
		// reads as made up (spec §8.1), is data to correct; a residence Bridge
		// does not serve is final, and no longer a warning.
		{"an ssn that failed the database check is a data mismatch",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("database_check_failed_on_social_security_number"),
			}},
			[]string{models.KycRejectionDataMismatch}, false},
		{"a fake-pattern tax id is a data mismatch",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("tax_identification_number_fake_pattern"),
			}},
			[]string{models.KycRejectionDataMismatch}, false},
		{"an unsupported geography is not eligible, and mapped",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("rejected_due_to_unsupported_geo"),
			}},
			[]string{models.KycRejectionNotEligible}, false},
		{"an unmapped issue is not eligible",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("selfie_verification_failed", "sanctions_screen_failed"),
			}},
			[]string{models.KycRejectionSelfieMismatch, models.KycRejectionNotEligible}, true},
		{"a duplicate of a field we do not map is not eligible",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
				endorsement("email_address.duplicate_customer_detected"),
			}},
			[]string{models.KycRejectionNotEligible}, true},
		{"developer reasons are the fallback when there is no issue",
			customerResponse{Status: "rejected", RejectionReasons: []rejectionReason{
				reason("Another customer with this tax ID number already exists. (customer: 11111111-1111-4111-8111-111111111111)"),
			}},
			[]string{models.KycRejectionTaxIDInUse}, false},
		{"an unmapped developer reason with no issue is not eligible",
			customerResponse{Status: "rejected", RejectionReasons: []rejectionReason{reason("Something Bridge never said before.")}},
			[]string{models.KycRejectionNotEligible}, true},
		// The sandbox rejects with nothing at all (measured on staging).
		{"a rejection with no reason at all is not eligible",
			customerResponse{Status: "rejected", Endorsements: []endorsementResponse{endorsement()}},
			[]string{models.KycRejectionNotEligible}, true},
		{"an offboarded customer is not eligible",
			customerResponse{Status: bridgeStatusOffboarded},
			[]string{models.KycRejectionNotEligible}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.customer.rejection()
			require.Equal(t, c.categories, got.categories)
			require.Equal(t, c.unmapped, len(got.unmapped) > 0, "unmapped: %v", got.unmapped)
		})
	}
}

// The sync path reads the same customer object the webhook carries, so a
// GET after a rejection persists the same categories.
func TestCustomerStateCarriesTheRejection(t *testing.T) {
	var envelope struct {
		EventObject json.RawMessage `json:"event_object"`
	}
	require.NoError(t, json.Unmarshal(readFixture(t, "rejected_tax_id_and_phone_duplicate.json"), &envelope))
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write(envelope.EventObject)
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, state.Status)
	require.Contains(t, state.RejectionCategories, models.KycRejectionTaxIDInUse)
	require.Len(t, state.RejectionDetails, 4)
}

// The region state on pix_onramp is not a reason for anything: a passport
// customer refused over a selfie is refused over the selfie, which they can
// fix, not made a dead end by the deposit they were never going to have.
func TestRejectionIgnoresAnEndorsementNotAvailableInTheRegion(t *testing.T) {
	resp := customerResponse{Status: "rejected", Endorsements: []endorsementResponse{
		{Name: "base", Status: "incomplete", Requirements: endorsementRequirements{
			Issues: []string{"selfie_verification_failed"}}},
		{Name: "pix_onramp", Status: "incomplete", Requirements: endorsementRequirements{
			Issues: []string{"endorsement_not_available_in_customers_region"}}},
	}}

	mapping := resp.rejection()
	require.Equal(t, []string{models.KycRejectionSelfieMismatch}, mapping.categories)
	require.Empty(t, mapping.unmapped)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return raw
}

// The rows written before the categories existed hold either shape in
// raw_status: the customer.updated delivery (webhook path) or the bare
// customer (sync path). The backfill reads both, the same way.
func TestRejectionFromSnapshotReadsBothShapes(t *testing.T) {
	delivery := readFixture(t, "rejected_selfie_phone_and_id_country.json")
	var envelope struct {
		EventObject json.RawMessage `json:"event_object"`
	}
	require.NoError(t, json.Unmarshal(delivery, &envelope))

	want := []string{models.KycRejectionSelfieMismatch, models.KycRejectionDocumentUnreadable, models.KycRejectionPhoneInUse}
	for name, raw := range map[string][]byte{"delivery": delivery, "customer": envelope.EventObject} {
		categories, details, err := RejectionFromSnapshot(raw)
		require.NoError(t, err, name)
		require.Equal(t, want, categories, name)
		require.Len(t, details, 3, name)
	}
}

func TestRejectionFromSnapshotOfACustomerNotRejected(t *testing.T) {
	categories, details, err := RejectionFromSnapshot([]byte(`{"id":"cus_1","status":"active"}`))
	require.NoError(t, err)
	require.Empty(t, categories)
	require.Empty(t, details)

	categories, _, err = RejectionFromSnapshot([]byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, categories, "a row that never heard from Bridge has nothing to map")
}

func TestRejectionFromSnapshotRefusesAMalformedSnapshot(t *testing.T) {
	_, _, err := RejectionFromSnapshot([]byte(`not json`))
	require.Error(t, err)
}
