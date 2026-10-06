package helius

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestCreateWebhook_SendsMethodPathAuthAndBody(t *testing.T) {
	const apiKey = "test-key"
	var (
		method string
		path   string
		key    string
		raw    []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		key = r.URL.Query().Get("api-key")
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webhookID":"wh-1"}`))
	}))
	t.Cleanup(srv.Close)

	provider := NewHeliusProvider(apiKey)
	provider.client = pointAt(t, srv)

	got, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		Network:    "mainnet",
		WebhookURL: "https://hooks.example/helius",
		Addresses:  []string{"So11111111111111111111111111111111111111112"},
		AuthSecret: "Bearer inbound",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "wh-1", got.ProviderWebhookID)
	assert.Equal(t, "Bearer inbound", got.SigningSecret)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/v0/webhooks", path)
	assert.Equal(t, apiKey, key)

	var body heliusWebhookBody
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, heliusWebhookBody{
		WebhookURL:       "https://hooks.example/helius",
		WebhookType:      heliusWebhookTypeMainnet,
		AccountAddresses: []string{"So11111111111111111111111111111111111111112"},
		TransactionTypes: []string{"ANY"},
		AuthHeader:       "Bearer inbound",
	}, body)
}

func TestCreateWebhook_MapsHTTPErrorsWithoutTheKeyOrHost(t *testing.T) {
	const apiKey = "test-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(srv.Close)

	provider := NewHeliusProvider(apiKey)
	provider.client = pointAt(t, srv)

	_, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		Network:    "mainnet",
		WebhookURL: "https://hooks.example/helius",
		AuthSecret: "Bearer inbound",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, chain.ErrRateLimited)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), "helius-rpc.com")
	assert.NotContains(t, err.Error(), "slow down")
	assert.NotContains(t, chain.CauseText(err), apiKey)
	assert.NotContains(t, chain.CauseText(err), "helius-rpc.com")
}

func pointAt(t *testing.T, srv *httptest.Server) *httpclient.Client {
	t.Helper()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return httpclient.Wrap(&http.Client{
		Timeout: 2 * time.Second,
		Transport: hostRewrite{
			target: target,
			base:   &http.Transport{Proxy: nil},
		},
	})
}

// hostRewrite sends the provider's fixed URL to the test server. The handler
// still sees the original method, path, query, headers, and body.
type hostRewrite struct {
	target *url.URL
	base   http.RoundTripper
}

func (r hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = r.target.Scheme
	clone.URL.Host = r.target.Host
	clone.Host = r.target.Host
	return r.base.RoundTrip(clone)
}
