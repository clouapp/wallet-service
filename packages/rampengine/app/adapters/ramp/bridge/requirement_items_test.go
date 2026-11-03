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

// An item that names no requirement is skipped: failing it would fail the whole
// customer decode, and with it GetKyc, SubmitApplication and the customer
// webhook for that person.
func TestRequirementItemsUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"string codes", `["terms_of_service_v1","sanctions_screen"]`, []string{"terms_of_service_v1", "sanctions_screen"}},
		{"object codes", `[{"type":"terms_of_service_v1"},{"requirement":"sanctions_screen"}]`,
			[]string{"terms_of_service_v1", "sanctions_screen"}},
		{"nested groups", `["government_id_document",{"any_of":["live_selfie","selfie_document"]}]`,
			[]string{"government_id_document", "live_selfie", "selfie_document"}},
		{"an empty string", `["a",""]`, []string{"a"}},
		{"a number", `["a",1]`, []string{"a"}},
		{"a nested group that is not a list", `["a",{"all_of":"b"}]`, []string{"a"}},
		{"null", `null`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var items requirementItems
			require.NoError(t, json.Unmarshal([]byte(tc.input), &items))
			require.Equal(t, tc.want, []string(items))
		})
	}
}

func TestDecodeCustomerWithObjectRequirementList(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{
			"id":"cus_pix","status":"incomplete",
			"endorsements":[{"name":"pix","status":"incomplete",
				"requirements":{"missing":{"all_of":[{"type":"terms_of_service_v1"}]}}}],
			"requirements_due":["proof_of_address"]
		}`))
	})

	state, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7},
		fullApplication(),
	)
	require.NoError(t, err)
	require.Equal(t, "/customers", got.path)
	require.Equal(t, models.KycStatusIncomplete, state.Status)
	require.Contains(t, state.Requirements, "terms_of_service_v1")
}

func TestAddressFromNormalizesBrazilSubdivision(t *testing.T) {
	payload := addressFrom(ramp.Address{
		City:        "Belo Horizonte",
		Subdivision: "Minas Gerais",
		Country:     "BRA",
	})
	require.Equal(t, "MG", payload.Subdivision)
}
