package ozav

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Ozav's rejection is final — "no address reverses a verdict" (its guides,
// 2026-09) — so every reason it gives is not_eligible, whatever the reason
// says: a category that reopened the wizard would send the user to fix
// something Ozav will not look at again. The reason code is kept as the
// detail, for the admin.
func TestCustomerRejection(t *testing.T) {
	cases := []struct {
		name       string
		customer   customerResponse
		categories []string
		details    []string
		unmapped   bool
	}{
		{"a customer that is not rejected has no category",
			customerResponse{Status: "pending", Decision: &customerDecision{Status: "information_requested", ReasonCode: "document_invalid"}},
			nil, nil, false},
		{"a documented rejection code",
			customerResponse{Status: "rejected", Decision: &customerDecision{Status: "rejected", ReasonCode: "identity_failed"}},
			[]string{models.KycRejectionNotEligible}, []string{"identity_failed"}, false},
		{"sanctions",
			customerResponse{Status: "rejected", Decision: &customerDecision{Status: "rejected", ReasonCode: "sanctions_match"}},
			[]string{models.KycRejectionNotEligible}, []string{"sanctions_match"}, false},
		{"a code Ozav added after this list",
			customerResponse{Status: "rejected", Decision: &customerDecision{Status: "rejected", ReasonCode: "brand_new_reason"}},
			[]string{models.KycRejectionNotEligible}, []string{"brand_new_reason"}, true},
		// Rejections recorded before Ozav had a vocabulary carry no decision.
		{"a rejection without a decision",
			customerResponse{Status: "rejected"},
			[]string{models.KycRejectionNotEligible}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.customer.rejection()
			require.Equal(t, c.categories, got.categories)
			require.Equal(t, c.details, got.details)
			require.Equal(t, c.unmapped, len(got.unmapped) > 0, "unmapped: %v", got.unmapped)
		})
	}
}

func TestGetKycCarriesTheRejection(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if ex.path == "/customers/ozav_cus_1/documents" {
			_, _ = w.Write([]byte(`{"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"rejected","verification":{"mode":"full"},` +
			`"decision":{"status":"rejected","reason_code":"inconsistent_data","reason":"Inconsistent data"},` +
			`"requirements":{"due":[],"missing_fields":[]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, state.Status)
	require.Equal(t, []string{models.KycRejectionNotEligible}, state.RejectionCategories)
	require.Equal(t, []string{"inconsistent_data"}, state.RejectionDetails)
}

func TestDecodeWebhookCarriesTheRejection(t *testing.T) {
	result, err := webhookAdapter("ozav_whsec_x").DecodeWebhook(context.Background(), []byte(
		`{"id":"ozav_evt_9","type":"customer.updated","created_at":"2026-09-28T18:00:00Z",`+
			`"data":{"id":"ozav_cus_1","status":"rejected","verification":{"mode":"full"},`+
			`"decision":{"status":"rejected","reason_code":"fraud_suspected"}}}`))
	require.NoError(t, err)
	require.Equal(t, models.KycStatusRejected, result.Status)
	require.Equal(t, []string{models.KycRejectionNotEligible}, result.RejectionCategories)
	require.Equal(t, []string{"fraud_suspected"}, result.RejectionDetails)
}

func writeEmailConflict(w http.ResponseWriter) {
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"error":{"type":"conflict_error","code":"CUSTOMER_EMAIL_ALREADY_EXISTS",` +
		`"message":"A customer with this email already exists.","param":"email","request_id":"req_email"}}`))
}

func requireRefusal(t *testing.T, err error, category, detail string) {
	t.Helper()
	var refusal *ramp.KycRefusal
	require.True(t, errors.As(err, &refusal), "want a refusal, got %v", err)
	require.Equal(t, []string{category}, refusal.Categories)
	require.Equal(t, []string{detail}, refusal.Details)
	// Still the provider's failure underneath, for the timeline and the code
	// the row records.
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// User 21 in production: the CPF belongs to another XIP account's Ozav
// customer. Before, the row went back to not_started and the app asked for
// the whole application again, forever.
func TestDuplicateHeldByAnotherAccountIsATaxIDRefusal(t *testing.T) {
	cpf := applicationTaxID(t)
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeDuplicateConflict(w)
			return
		}
		other := customerWithTaxID("ozav_cus_other", cpf)
		other["email"] = "other@example.com"
		writeCustomerPage(w, "", other)
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	requireRefusal(t, err, models.KycRejectionTaxIDInUse, codeCustomerAlreadyExists)
}

func TestDuplicateNobodyCanFindIsATaxIDRefusal(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeDuplicateConflict(w)
			return
		}
		writeCustomerPage(w, "", customerWithTaxID("ozav_cus_other", "11122233344"))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	requireRefusal(t, err, models.KycRejectionTaxIDInUse, codeCustomerAlreadyExists)
}

// The lookup that decides adoption failing is not an answer about the
// customer: the next submission may adopt it, so nothing is refused.
func TestDuplicateWhoseLookupFailsIsNotARefusal(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeDuplicateConflict(w)
			return
		}
		writeInternalError(w)
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	var refusal *ramp.KycRefusal
	require.False(t, errors.As(err, &refusal))
	require.Contains(t, err.Error(), codeCustomerAlreadyExists)
}

// User 14 in production: Ozav holds another customer under this e-mail.
func TestEmailAlreadyExistsIsANotEligibleRefusal(t *testing.T) {
	adapter, seen := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		writeEmailConflict(w)
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	requireRefusal(t, err, models.KycRejectionNotEligible, codeCustomerEmailAlreadyExists)
	require.Len(t, *seen, 1, "nothing to adopt: the e-mail is the account's own")
}

// A create that failed for a reason unrelated to who the customer is keeps
// today's behaviour: the row goes back to what it was, and a retry may pass.
func TestTransientCreateFailureIsNotARefusal(t *testing.T) {
	adapter, _ := scriptedAdapter(t, func(i int, ex exchange, w http.ResponseWriter) {
		if i == 0 {
			writeInternalError(w)
			return
		}
		writeCustomerPage(w, "")
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.Error(t, err)
	var refusal *ramp.KycRefusal
	require.False(t, errors.As(err, &refusal))
}

// The two create refusals stored in production (users 14 and 21), as the
// row's last_error_detail holds them, with the request ids replaced.
func TestCreateRefusalFromDetail(t *testing.T) {
	cases := []struct {
		detail     string
		categories []string
		ok         bool
	}{
		{"ozav: POST /customers: upstream rejected (CUSTOMER_EMAIL_ALREADY_EXISTS) param=email request_id=00000000-0000-4000-8000-000000000013",
			[]string{models.KycRejectionNotEligible}, true},
		{"ozav: POST /customers: upstream rejected (CUSTOMER_ALREADY_EXISTS) request_id=00000000-0000-4000-8000-000000000015",
			[]string{models.KycRejectionTaxIDInUse}, true},
		{"ozav: POST /customers: upstream rejected (INTERNAL_ERROR) request_id=req_1", nil, false},
		// A refusal of an update is not a refusal of the person.
		{"ozav: PATCH /customers/ozav_cus_1: upstream rejected (CUSTOMER_ALREADY_EXISTS)", nil, false},
		{"bridge: POST /customers: upstream rejected (invalid_parameters)", nil, false},
		{"", nil, false},
	}
	for _, c := range cases {
		categories, details, ok := CreateRefusalFromDetail(c.detail)
		require.Equal(t, c.ok, ok, c.detail)
		require.Equal(t, c.categories, categories, c.detail)
		if ok {
			require.Len(t, details, 1)
		}
	}
}

func TestRejectionFromSnapshotReadsBothShapes(t *testing.T) {
	customer := `{"id":"ozav_cus_1","status":"rejected","verification":{"mode":"full"},` +
		`"decision":{"status":"rejected","reason_code":"policy"}}`
	delivery := `{"id":"ozav_evt_1","type":"customer.updated","data":` + customer + `}`
	for _, raw := range []string{customer, delivery} {
		categories, details, err := RejectionFromSnapshot([]byte(raw))
		require.NoError(t, err)
		require.Equal(t, []string{models.KycRejectionNotEligible}, categories)
		require.Equal(t, []string{"policy"}, details)
	}

	categories, _, err := RejectionFromSnapshot([]byte(`{"id":"ozav_cus_1","status":"pending"}`))
	require.NoError(t, err)
	require.Empty(t, categories)
}
