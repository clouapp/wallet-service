package blindpay_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

func envelope(code, message string) string {
	return `{"success":false,"code":"` + code + `","message":"` + message +
		`","description":"Contains the submitted value 123.456.789-00","errors":[{"field":"tax_id","value":"123.456.789-00"}],"trace_id":"0af7651916cd43dd"}`
}

func TestClassify_MapsTheCodesACallerActsOn(t *testing.T) {
	cases := []struct {
		status        int
		code, message string
		want          error
	}{
		{401, "AUTH_UNAUTHORIZED", "", ramp.ErrProviderCredentialsInvalid},
		{404, "INSTANCES_NOT_FOUND", "", ramp.ErrProviderCredentialsInvalid},
		{403, "AUTH_FORBIDDEN", "", ramp.ErrProviderNotImplemented},
		{429, "AUTH_RATE_LIMITED", "", ramp.ErrRetryableUpstream},
		{500, "INTERNAL_ERROR", "", ramp.ErrRetryableUpstream},
		{502, "QUOTES_RATE_UNAVAILABLE", "", ramp.ErrRetryableUpstream},
		{400, "TOS_NOT_ACCEPTED", "", ramp.ErrTosRequired},
		{404, "TOS_NOT_FOUND", "", ramp.ErrTosSessionUnknown},
		{400, "QUOTES_EXPIRED", "quote_expired", ramp.ErrEstimateExpired},
		{409, "QUOTES_ALREADY_USED", "quote_already_used", ramp.ErrEstimateConsumed},
		{404, "CUSTOMERS_NOT_FOUND", "", ramp.ErrIntegrationRequired},
		{403, "CUSTOMERS_KYC_NOT_APPROVED", "", ramp.ErrKycPending},
		{400, "PAYOUTS_AMOUNT_BELOW_MINIMUM", "", ramp.ErrAmountBelowMinimum},
		{400, "QUOTES_AMOUNT_BELOW_PROVIDER_MINIMUM", "", ramp.ErrAmountBelowMinimum},
		{400, "BLOCKCHAIN_NETWORK_NOT_SUPPORTED", "", ramp.ErrNoProviderAvailable},
		{400, "BLOCKCHAIN_TOKEN_NOT_SUPPORTED", "", ramp.ErrNoProviderAvailable},
		// The legacy message carries what no code names yet.
		{400, "VALIDATION_INVALID_REQUEST", "please_accept_terms_of_service", ramp.ErrTosRequired},
		{422, "", "idempotency_key_payload_mismatch", ramp.ErrIdempotencyConflict},
		{409, "", "idempotency_key_in_flight", ramp.ErrRetryableUpstream},
		// No code at all: the status is all there is.
		{503, "", "", ramp.ErrRetryableUpstream},
		{504, "", "", ramp.ErrRetryableUpstream},
	}
	for _, c := range cases {
		client, _ := newTestClient(t, c.status, envelope(c.code, c.message))
		err := client.Do(context.Background(), blindpay.Request{Method: http.MethodPost, Path: "quotes"}, nil)
		assert.ErrorIs(t, err, c.want, "%d %s %s", c.status, c.code, c.message)
		assert.ErrorIs(t, err, ramp.ErrProviderFailed, "every rejection is a provider failure")
	}
}

// An unknown code stays opaque rather than being read as a condition it may
// not be: it is a provider failure and nothing else.
func TestClassify_AnUnknownCodeIsOnlyAProviderFailure(t *testing.T) {
	client, _ := newTestClient(t, 400, envelope("VALIDATION_FAILED", "validation_failed"))
	err := client.Do(context.Background(), blindpay.Request{Method: http.MethodPost, Path: "quotes"}, nil)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	for _, sentinel := range []error{
		ramp.ErrRetryableUpstream, ramp.ErrTosRequired, ramp.ErrEstimateExpired, ramp.ErrIntegrationRequired,
	} {
		assert.NotErrorIs(t, err, sentinel)
	}
}

// description and errors[] can restate what was submitted. They never reach
// Error(), which LogUpstreamCall writes verbatim.
func TestClassify_ErrorCarriesNoSubmittedValue(t *testing.T) {
	client, _ := newTestClient(t, 400, envelope("VALIDATION_FAILED", "validation_failed"))
	err := client.Do(context.Background(), blindpay.Request{Method: http.MethodPost, Path: "customers"}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "123.456.789-00")
	assert.Contains(t, err.Error(), "VALIDATION_FAILED")
	assert.Contains(t, err.Error(), "0af7651916cd43dd")

	var upstream *blindpay.UpstreamError
	require.True(t, errors.As(err, &upstream))
	assert.Equal(t, "0af7651916cd43dd", upstream.ProviderRequestID())
	assert.Equal(t, 400, upstream.Status)
}

func TestClassify_ABodyThatIsNotTheEnvelope(t *testing.T) {
	for _, body := range []string{"", "<html>bad gateway</html>", `{"unexpected":true}`} {
		client, _ := newTestClient(t, 502, body)
		err := client.Do(context.Background(), blindpay.Request{Method: http.MethodGet, Path: "customers"}, nil)
		var upstream *blindpay.UpstreamError
		require.True(t, errors.As(err, &upstream), "body %q", body)
		assert.Equal(t, "http_502", upstream.Code)
		assert.ErrorIs(t, err, ramp.ErrRetryableUpstream)
		assert.False(t, strings.Contains(err.Error(), "<html>"))
	}
}

// The fields the classifier reads must exist on BlindPay's error envelope.
func TestClassify_EnvelopeMatchesTheContract(t *testing.T) {
	type errorEnvelopeMirror struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	}
	requireFieldsInSchema(t, "POST", "/v1/instances/{instance_id}/quotes", 400, errorEnvelopeMirror{})
}
