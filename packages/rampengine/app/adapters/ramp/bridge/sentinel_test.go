package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// statusAdapter answers every call with one status and body, which is all a
// classification test needs.
func statusAdapter(t *testing.T, status int, body string) (*Adapter, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil), server
}

// Bridge's rejections used to reach the app as one undifferentiated 502
// provider_unavailable, because UpstreamError carried no sentinel and
// resources.ErrorCode matches by sentinel only. That is the right answer for
// a rejection XIP cannot explain, and the wrong one for the rejections it
// can: a compliance refusal is the user's own KYC, not an outage, and
// telling them the service is down hides the only thing they can act on.
//
// Only codes measured against Bridge are mapped. An unrecognised one keeps
// the generic answer on purpose — guessing at a vocabulary is how a fault on
// our side starts being reported as the user's.
func TestUpstreamErrorCarriesASentinelForComplianceRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{
			// Measured 01/09: POST /customers/{id}/external_accounts for a
			// customer whose endorsements do not cover the rail.
			name:   "missing endorsements is the user's KYC, not an outage",
			status: http.StatusUnprocessableEntity,
			body:   `{"code":"missing_required_endorsements","message":"customer is missing endorsements"}`,
			want:   ramp.ErrKycPending,
		},
		{
			name:   "a refused key is ours to rotate",
			status: http.StatusUnauthorized,
			body:   `{"code":"unauthorized","message":"bad key"}`,
			want:   ramp.ErrProviderCredentialsInvalid,
		},
		{
			name:   "a rail the account is not enabled for is not a refused key",
			status: http.StatusUnauthorized,
			body:   `{"code":"not_allowed","message":"Requested rail or feature is not enabled"}`,
			want:   ramp.ErrProviderFeatureDisabled,
		},
		{
			name:   "a throttle that answered is worth repeating",
			status: http.StatusTooManyRequests,
			body:   `{"code":"rate_limited","message":"slow down"}`,
			want:   ramp.ErrRetryableUpstream,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _ := statusAdapter(t, tc.status, tc.body)
			_, err := adapter.EstimateOfframp(context.Background(),
				ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// A code Bridge has that this side has never measured must not be guessed at.
func TestUnknownUpstreamCodeStaysGeneric(t *testing.T) {
	adapter, _ := statusAdapter(t, http.StatusBadRequest,
		`{"code":"some_new_code","message":"whatever"}`)
	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
	require.Error(t, err)
	for _, sentinel := range []error{
		ramp.ErrKycPending, ramp.ErrRetryableUpstream, ramp.ErrProviderCredentialsInvalid,
		ramp.ErrIntegrationRequired, ramp.ErrNoProviderAvailable,
	} {
		require.NotErrorIs(t, err, sentinel)
	}
}

// The throttle with an empty body keeps its existing meaning: provably
// pre-send, and therefore the one Bridge failure a create may fail over from.
func TestEmptyThrottleStaysPreSend(t *testing.T) {
	adapter, _ := statusAdapter(t, http.StatusTooManyRequests, "")
	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
}
