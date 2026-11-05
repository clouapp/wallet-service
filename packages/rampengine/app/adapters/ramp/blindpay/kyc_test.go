package blindpay_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const customerPrefix = "/v1/instances/in_000000000001/customers/cu_1"

const customerBody = `{"id":"cu_1","customer_id":"cu_1","kyc_type":"standard","kyc_status":"compliance_request","tos_id":"to_1","email":"a@b.co","limit":{"per_transaction":1,"daily":1,"monthly":1}}`

// routedServer answers by path and counts every request. It returns an adapter
// pointed at it.
func routedServer(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*blindpay.Adapter, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler, ok := routes[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "bp_test_key", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
	return blindpay.New(clientFactory(client, nil), nil, nil), &hits
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func TestStartIntegration_WithoutACustomerCallsNothing(t *testing.T) {
	adapter, hits := routedServer(t, nil)
	got, err := adapter.StartIntegration(context.Background(), ramp.CustomerRef{CustomerID: 1})
	require.NoError(t, err)
	assert.Equal(t, models.IntegrationStatusPending, got.Status)
	assert.Zero(t, hits.Load())
}

func TestRefreshIntegration_ReadsTheCustomer(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){customerPrefix: reply(customerBody)})
	got, err := adapter.StartIntegration(context.Background(), ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"})
	require.NoError(t, err)
	assert.Equal(t, "cu_1", got.ExternalCustomerID)
	assert.Equal(t, models.IntegrationStatusActive, got.Status)
}

func TestGetKyc_ReadsCustomerAndRFI(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(customerBody),
		customerPrefix + "/rfi": reply(`{"status":"pending","request":[{"title":"t","fields":[{"key":"source_of_funds_doc","label":"l","required":true}]}]}`),
	})
	ref := ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"}
	state, err := adapter.GetKyc(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusRequiresInfo, state.Status)
	assert.Equal(t, []string{"source_of_funds_doc"}, state.Requirements)
	assert.Equal(t, "cu_1", state.ExternalCustomerID)
	assert.Equal(t, "a@b.co", state.CustomerEmail)
	require.Len(t, state.Endorsements, 2)
	assert.Equal(t, models.EndorsementBankPayout, state.Endorsements[0].Name)
	assert.Equal(t, models.EndorsementPix, state.Endorsements[1].Name)
	assert.Equal(t, models.EndorsementStatusIncomplete, state.Endorsements[0].Status)

	reqs, err := adapter.GetRequirements(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, []string{"source_of_funds_doc"}, reqs.Missing)
	assert.Equal(t, models.KycStatusRequiresInfo, reqs.Status)
}

func TestGetKyc_NoOpenRFIIsNull(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(customerBody),
		customerPrefix + "/rfi": reply(`null`),
	})
	state, err := adapter.RefreshKyc(context.Background(), ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"})
	require.NoError(t, err)
	assert.Empty(t, state.Requirements)
}

func TestGetKyc_RequiresACustomer(t *testing.T) {
	adapter, hits := routedServer(t, nil)
	_, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 1})
	assert.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Zero(t, hits.Load())
}

func TestRequestEndorsements_RejectsUnknownNames(t *testing.T) {
	adapter, hits := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(customerBody),
		customerPrefix + "/rfi": reply(`null`),
	})
	ref := ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"}
	_, err := adapter.RequestEndorsements(context.Background(), ref, []string{"pix_onramp"})
	assert.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	assert.Zero(t, hits.Load())

	state, err := adapter.RequestEndorsements(context.Background(), ref, []string{"bank_payout"})
	require.NoError(t, err)
	assert.Equal(t, "cu_1", state.ExternalCustomerID)
	assert.NotZero(t, hits.Load())
}

func TestRequestEndorsements_PixIsAReadOfTheCurrentState(t *testing.T) {
	adapter, hits := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(customerBody),
		customerPrefix + "/rfi": reply(`null`),
	})
	ref := ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"}
	state, err := adapter.RequestEndorsements(context.Background(), ref, []string{"pix"})
	require.NoError(t, err)
	assert.Equal(t, "cu_1", state.ExternalCustomerID)
	assert.NotZero(t, hits.Load())
}

func TestSubmitApplicationAndDocumentsAreNotTheBlindPayPath(t *testing.T) {
	adapter, hits := routedServer(t, nil)
	ref := ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"}
	_, err := adapter.SubmitApplication(context.Background(), ref, ramp.Application{})
	assert.ErrorIs(t, err, ramp.ErrDocumentsNotSupported)
	assert.Contains(t, err.Error(), "blindpay creates customers atomically; KYC cannot be updated")
	_, err = adapter.SubmitDocuments(context.Background(), ref, ramp.Documents{})
	assert.ErrorIs(t, err, ramp.ErrDocumentsNotSupported)
	assert.Zero(t, hits.Load())
}

func TestCustomerResponseMatchesTheContract(t *testing.T) {
	requireFieldsInSchema(t, "GET", "/v1/instances/{instance_id}/customers/{id}", 200, blindpay.CustomerResponse{})
}

func failing(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `{"success":false,"message":"boom"}`)
}

// The RFI is what the customer owes, not who they are: a failed read of it
// still answers the customer when they are not waiting on one.
func TestGetKyc_RFIFailureReturnsCustomerState(t *testing.T) {
	approved := `{"id":"cu_1","customer_id":"cu_1","kyc_type":"standard","kyc_status":"approved","tos_id":"to_1","email":"a@b.co","limit":{"per_transaction":1,"daily":1,"monthly":1}}`
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(approved),
		customerPrefix + "/rfi": failing,
	})
	state, err := adapter.RefreshKyc(context.Background(), ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"})
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusApproved, state.Status)
	assert.Empty(t, state.Requirements)
}

// A customer in compliance_request is waiting on exactly the RFI, and the sync
// writes Requirements as read: degrading would erase the pending steps.
func TestGetKyc_RFIFailureFailsWhileInformationIsRequested(t *testing.T) {
	adapter, _ := routedServer(t, map[string]func(http.ResponseWriter, *http.Request){
		customerPrefix:          reply(customerBody),
		customerPrefix + "/rfi": failing,
	})
	_, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 1, ExternalID: "cu_1"})
	assert.Error(t, err)
}
