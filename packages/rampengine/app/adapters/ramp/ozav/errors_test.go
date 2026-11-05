package ozav

import (
	"strings"
	"testing"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"

	"github.com/stretchr/testify/require"
)

// classify's contract, and the reason this file exists at all: the first
// live smoke against the real API logged only "upstream rejected
// (INVALID_REQUEST)" and reproducing the failure by hand (a curl the sandbox
// key answered) was the only way to learn which field Ozav rejected. Whoever
// debugs this in production will not have the key to do that. Method, param
// and request_id are safe to log — they name a field and an opaque
// identifier, never a value — so they must reach both Error() and the
// struct. Message and Details[].Message can restate the rejected value
// (Ozav's validation errors echo submitted PII), so they are never decoded.

func TestClassifyDecodesCodeParamAndRequestID(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","code":"INVALID_REQUEST",
		"message":"Request body failed validation.","param":"individual.legal_name",
		"request_id":"req_9f8e7d"}}`)

	err := classify("POST", "/customers", 422, body)

	upstream, ok := err.(*UpstreamError)
	require.True(t, ok)
	require.Equal(t, "INVALID_REQUEST", upstream.Code)
	require.Equal(t, "individual.legal_name", upstream.Param)
	require.Equal(t, "req_9f8e7d", upstream.RequestID)
}

// The exact shape the first live smoke actually hit: no top-level param, only
// a field-level one inside details. Param must still surface so the log line
// names the failing field without anyone having to open Details.
func TestClassifyFallsBackToDetailParamWhenTopLevelParamIsAbsent(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","code":"INVALID_REQUEST",
		"message":"Request body failed validation.",
		"details":[{"code":"invalid_field","message":"Required","param":"individual.legal_name"}]}}`)

	err := classify("POST", "/customers", 422, body)

	upstream, ok := err.(*UpstreamError)
	require.True(t, ok)
	require.Equal(t, "individual.legal_name", upstream.Param)
}

func TestClassifyFallsBackToHTTPStatusWhenBodyHasNoCode(t *testing.T) {
	err := classify("GET", "/customers/cus_1", 500, []byte("not json"))

	upstream, ok := err.(*UpstreamError)
	require.True(t, ok)
	require.Equal(t, "http_500", upstream.Code)
}

func TestUpstreamErrorMessageIncludesParamAndRequestID(t *testing.T) {
	upstream := &UpstreamError{
		Method: "POST", Path: "/customers", Code: "INVALID_REQUEST",
		Param: "individual.legal_name", RequestID: "req_9f8e7d",
	}

	got := upstream.Error()

	require.Contains(t, got, "POST")
	require.Contains(t, got, "/customers")
	require.Contains(t, got, "INVALID_REQUEST")
	require.Contains(t, got, "individual.legal_name")
	require.Contains(t, got, "req_9f8e7d")
}

// The constraint that matters most in this file: message and details[].message
// can echo submitted PII (name, CPF, address), so neither may ever reach the
// string that ends up in facades.Log() via ramp.LogUpstreamCall.
func TestUpstreamErrorMessageNeverIncludesMessageOrDetailMessages(t *testing.T) {
	err := classify("POST", "/customers", 422, []byte(`{"error":{"code":"INVALID_REQUEST",
		"param":"individual.legal_name","request_id":"req_9f8e7d",
		"message":"super-secret-pii-value-should-not-log",
		"details":[{"code":"invalid_field","param":"individual.legal_name",
			"message":"another-pii-value-should-not-log"}]}}`))

	require.False(t, strings.Contains(err.Error(), "super-secret-pii-value-should-not-log"))
	require.False(t, strings.Contains(err.Error(), "another-pii-value-should-not-log"))
}

// §8 of the design spec: every rejection a caller can act on must reach it as
// a ramp sentinel, not as a code string it would have to know about. The
// table is the contract — one case per row, and the default is deliberately
// the opaque one, so a code nobody has seen yet fails to the safe side
// instead of being read as something specific.
//
// This covers the per-operation `code`. The high-level `type` is a separate
// field with a separate table, in TestClassifyMapsErrorTypesToSentinels
// below — the two are not interchangeable, and conflating them is how the
// 401 mapping silently never fired.
func TestClassifyMapsCodesToSentinels(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		status int
		code   string
		want   error
	}{
		{"403 missing entitlement is not resolvable at runtime", "/wallets", 403, "ENTITLEMENT_REQUIRED", ramp.ErrProviderNotImplemented},
		{"403 insufficient scope, likewise", "/customers", 403, "INSUFFICIENT_SCOPE", ramp.ErrProviderNotImplemented},
		{"404 on a customer means the integration is gone", "/customers/ozav_cus_1", 404, "NOT_FOUND", ramp.ErrIntegrationRequired},
		{"409 reused idempotency key is our collision", "/transfers", 409, "IDEMPOTENCY_KEY_REUSED", ramp.ErrIdempotencyConflict},
		{"422 provisioning is the Pix corridor", "/transfers", 422, "PROVISIONING_REQUIRED", ramp.ErrOzavPayoutNotProvisioned},
		{"422 expired quote is a re-quote", "/transfers", 422, "QUOTE_EXPIRED", ramp.ErrEstimateExpired},
		{"422 amount below Ozav minimum is the client minimum error", "/quotes", 422, "AMOUNT_BELOW_MINIMUM", ramp.ErrAmountBelowMinimum},
		// The request is incomplete; provider_unavailable would blame an
		// outage that is not happening.
		{"422 missing destination chain is a request to re-quote", "/transfers", 422, "DESTINATION_CHAIN_REQUIRED", ramp.ErrEstimateMismatch},
		{"429 is retryable", "/quotes", 429, "RATE_LIMITED", ramp.ErrRetryableUpstream},
		{"501 is not implemented", "/cards", 501, "CARDS_NOT_IMPLEMENTED", ramp.ErrProviderNotImplemented},
		{"503 api disabled is retryable", "/customers", 503, "API_V2_DISABLED", ramp.ErrRetryableUpstream},

		// The rest of the 422 catalogue Ozav's own ErrorEnvelope documents on
		// POST /transfers. Every one of them used to resolve to nil and reach
		// the app as 502 provider_unavailable — "the service is down" — when
		// each says something the app already has a truthful screen for.
		{"a corridor with no coverage is not offered", "/transfers", 422, "RAIL_NOT_AVAILABLE", ramp.ErrNoProviderAvailable},
		{"a leg that is not available yet is not offered", "/transfers", 422, "NOT_AVAILABLE", ramp.ErrNoProviderAvailable},
		{"a target currency not carried is not offered", "/quotes", 422, "TARGET_CURRENCY_NOT_AVAILABLE", ramp.ErrNoProviderAvailable},
		{"an undefined pair is not offered", "/transfers", 422, "UNSUPPORTED_COMBINATION", ramp.ErrNoProviderAvailable},
		// A destination Ozav has not finished enabling is the same condition
		// PROVISIONING_REQUIRED describes, seen from the other side. Both
		// clear by themselves, so both are the retry instruction.
		{"a destination not yet enabled clears itself", "/transfers", 422, "CUSTOMER_NOT_ENABLED", ramp.ErrRetryableUpstream},
		// Live only: the sandbox simulates the terms attestation, so the first
		// real customer create was the first time this code was ever seen. It
		// is the same condition Bridge names has_not_accepted_tos, and the
		// same sentinel — the terms of service were not signed. Unmapped it
		// reached the user as "provider unavailable, try again later", for a
		// refusal that answers identically until someone accepts the terms.
		{"422 terms not accepted is an unsigned terms of service", "/customers", 422, "TERMS_ACCEPTANCE_REQUIRED", ramp.ErrTosRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"error":{"code":"` + tc.code + `","message":"x","request_id":"req_1"}}`)

			err := classify("POST", tc.path, tc.status, body)

			require.ErrorIs(t, err, tc.want)
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream, "the structured detail survives the sentinel")
			require.Equal(t, "req_1", upstream.RequestID)
		})
	}
}

// A 404 that is not about a customer is not an integration problem. Reading
// every 404 as ErrIntegrationRequired would tell the client to re-run KYC
// because a quote id was mistyped.
func TestClassifyOnlyReadsCustomer404AsIntegrationRequired(t *testing.T) {
	err := classify("GET", "/transfers/ozav_trf_1", 404, []byte(`{"error":{"code":"NOT_FOUND"}}`))

	require.NotErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// An unrecognized code must not resolve to any sentinel: a caller branching
// on one would act on a guess. It stays an UpstreamError and nothing else.
func TestClassifyLeavesUnknownCodesOpaque(t *testing.T) {
	err := classify("POST", "/customers", 418, []byte(`{"error":{"code":"TEAPOT"}}`))

	for _, sentinel := range []error{
		ramp.ErrProviderCredentialsInvalid, ramp.ErrProviderNotImplemented,
		ramp.ErrIntegrationRequired, ramp.ErrIdempotencyConflict,
		ramp.ErrOzavPayoutNotProvisioned, ramp.ErrEstimateExpired,
		ramp.ErrRetryableUpstream,
	} {
		require.NotErrorIs(t, err, sentinel)
	}
}

// The envelope has two identifiers and they are not interchangeable: `type`
// is the high-level class Ozav documents as a closed list, `code` is the
// per-operation detail. Reading a class name out of the code field matches
// nothing — 401 carries type "authentication_error" with an operation code
// beside it, so a mapping keyed on the code alone never fires and the one
// rejection an admin can fix reads as a generic upstream failure.
func TestClassifyMapsErrorTypesToSentinels(t *testing.T) {
	cases := []struct {
		name      string
		errorType string
		status    int
		want      error
	}{
		{"a key that is missing, revoked or expired", "authentication_error", 401, ramp.ErrProviderCredentialsInvalid},
		{"authenticated but not entitled", "permission_error", 403, ramp.ErrProviderNotImplemented},
		{"throttled", "rate_limit_error", 429, ramp.ErrRetryableUpstream},
		{"the idempotency layer was unreachable", "idempotency_error", 503, ramp.ErrRetryableUpstream},
		{"an unexpected server-side error", "api_error", 500, ramp.ErrRetryableUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"error":{"type":"` + tc.errorType + `","code":"SOME_OPERATION_CODE",
				"message":"x","request_id":"req_1"}}`)

			err := classify("POST", "/customers", tc.status, body)

			require.ErrorIs(t, err, tc.want)
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			require.Equal(t, tc.errorType, upstream.Type, "the class is kept for whoever debugs this")
		})
	}
}

// The code is the more specific of the two, so it wins. A conflict_error
// carrying IDEMPOTENCY_KEY_REUSED is our collision, not a generic conflict.
func TestClassifyPrefersTheCodeOverTheType(t *testing.T) {
	body := []byte(`{"error":{"type":"conflict_error","code":"IDEMPOTENCY_KEY_REUSED","request_id":"req_1"}}`)

	err := classify("POST", "/transfers", 409, body)

	require.ErrorIs(t, err, ramp.ErrIdempotencyConflict)
}

// The 501 codes Ozav documents as platform-wide reach the same sentinel as
// the status does, and naming them keeps the mapping true if the status ever
// arrives as something else.
func TestClassifyMapsTheDocumentedNotImplementedCodes(t *testing.T) {
	for _, code := range []string{"PROVISIONING_NOT_IMPLEMENTED", "VERIFICATION_SESSION_NOT_IMPLEMENTED"} {
		err := classify("POST", "/wallets", 501,
			[]byte(`{"error":{"type":"invalid_request_error","code":"`+code+`"}}`))

		require.ErrorIs(t, err, ramp.ErrProviderNotImplemented, code)
	}
}
