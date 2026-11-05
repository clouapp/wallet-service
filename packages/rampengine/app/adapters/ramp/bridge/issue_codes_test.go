package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Bridge's issues mix bare codes with objects that key a list of codes by the
// field they concern ({"place_of_birth":["place_of_birth_missing"]}, measured
// on the sandbox on 07/10/2026). Failing to read one would fail the whole
// customer decode — on a create, losing the customer Bridge just made — so
// every shape is read for the codes it carries and anything else is skipped.
func TestIssueCodesUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"bare codes", `["selfie_verification_failed","id_expired"]`, []string{"selfie_verification_failed", "id_expired"}},
		{"the sandbox's mixed shape", `[{"place_of_birth":["place_of_birth_missing"]},"tax_id_issuing_country_mismatch"]`,
			[]string{"place_of_birth_missing", "tax_id_issuing_country_mismatch"}},
		{"an object's keys in the order sent", `[{"zeta":["z1","z2"],"alpha":["a1"]}]`, []string{"z1", "z2", "a1"}},
		{"an object naming a single code", `[{"nationalities":"nationalities_missing"}]`, []string{"nationalities_missing"}},
		{"a code repeated across items", `["a",{"f":["a","b"]},"b"]`, []string{"a", "b"}},
		{"empty strings, numbers and nested objects", `["",1,{"f":[2,"",{"g":"x"}],"h":{"i":["y"]}},"a"]`, []string{"a"}},
		{"an empty list", `[]`, nil},
		{"null", `null`, nil},
		{"not a list at all", `{"place_of_birth":["place_of_birth_missing"]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var codes issueCodes
			require.NoError(t, json.Unmarshal([]byte(tc.input), &codes))
			require.Equal(t, tc.want, []string(codes))
		})
	}
}

// The create answer the sandbox gave a CPF holder living in Portugal (smoke
// scenario 4, ids and names replaced): every endorsement incomplete, each
// issues list the mixed shape. The customer is read — its id kept, so the
// next call updates it instead of orphaning it — and the codes reach the
// endorsements as requirements.
func TestSubmitApplicationReadsACustomerWithObjectIssues(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write(readFixture(t, "customer_cpf_prt_eea_incomplete.json"))
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)

	require.Equal(t, "cus_cpf_prt_incomplete", state.ExternalCustomerID)
	require.Equal(t, models.KycStatusIncomplete, state.Status)
	onramp := endorsementNamed(t, state, models.EndorsementPixOnramp)
	require.Equal(t, models.EndorsementStatusIncomplete, onramp.Status)
	require.Subset(t, onramp.Requirements, []string{"place_of_birth_missing", "tax_id_issuing_country_mismatch"})
	require.Subset(t, state.Requirements, []string{"place_of_birth_missing", "tax_id_issuing_country_mismatch"})
}

// The rejection reads the object issues' codes like the bare ones: the same
// customer refused is refused over both, each named for the warning that
// asks for it to be mapped.
func TestRejectionReadsObjectIssues(t *testing.T) {
	var resp customerResponse
	require.NoError(t, json.Unmarshal(readFixture(t, "customer_cpf_prt_eea_incomplete.json"), &resp))
	require.Equal(t, []string{"place_of_birth_missing", "tax_id_issuing_country_mismatch"}, resp.issues())

	resp.Status = "rejected"
	mapping := resp.rejection()
	require.Equal(t, []string{models.KycRejectionNotEligible}, mapping.categories)
	require.Equal(t, []string{"place_of_birth_missing", "tax_id_issuing_country_mismatch"}, mapping.unmapped)
}

// A business customer's requirements key a partner's codes by the partner
// ({"associated_person": "ap_1", "items": [...]}, and missing's
// {"object_type": ..., "object_id": ...}, sandbox 18/10/2026). The id is no
// code: read as one, it reached the rejection as an unmapped issue, which is
// not_eligible — terminal — and the company would never be sent again.
func TestIssueCodesSkipThePersonAnIssueIsAbout(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"a partner's issues", `[{"associated_person":"ap_1","items":["id_expired"]},"business_registry_no_match"]`,
			[]string{"id_expired", "business_registry_no_match"}},
		{"missing's object shape", `[{"object_type":"associated_person","object_id":"ap_1","all_of":["selfie_verification_failed"]}]`,
			[]string{"selfie_verification_failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var codes issueCodes
			require.NoError(t, json.Unmarshal([]byte(tc.input), &codes))
			require.Equal(t, tc.want, []string(codes))
		})
	}

	rejected := customerResponse{Status: "rejected", Endorsements: []endorsementResponse{{Name: "pix_onramp"}}}
	require.NoError(t, json.Unmarshal([]byte(`[{"associated_person":"ap_1","items":["id_expired"]}]`), &rejected.Endorsements[0].Requirements.Issues))
	mapping := rejected.rejection()
	require.Equal(t, []string{models.KycRejectionDocumentUnreadable}, mapping.categories, "a partner's expired ID is a document to send again")
	require.Empty(t, mapping.unmapped)
}
