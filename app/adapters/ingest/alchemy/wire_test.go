package alchemy

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

func TestCreate_Webhook_SendsMethodPathAuthAndBody(t *testing.T) {
	const apiKey = "test-key"
	var (
		method string
		path   string
		token  string
		raw    []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		token = r.Header.Get(alchemyAuthTokenHdr)
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"wh-1","signing_key":"sign-1"}}`))
	}))
	t.Cleanup(srv.Close)

	provider := NewAlchemyProvider(apiKey)
	provider.client = pointAt(t, srv)

	got, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		Network:    "ETH_MAINNET",
		WebhookURL: "https://hooks.example/alchemy",
		Addresses:  []string{"0xabc"},
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "wh-1", got.ProviderWebhookID)
	assert.Equal(t, "sign-1", got.SigningSecret)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/create-webhook", path)
	assert.Equal(t, apiKey, token)

	var body alchemyCreateReq
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, alchemyCreateReq{
		Network:     "ETH_MAINNET",
		WebhookType: "ADDRESS_ACTIVITY",
		WebhookURL:  "https://hooks.example/alchemy",
		Addresses:   []string{"0xabc"},
	}, body)
}

func TestCreate_Webhook_MapsHTTPErrorsWithoutTheKeyOrHost(t *testing.T) {
	const apiKey = "test-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(srv.Close)

	provider := NewAlchemyProvider(apiKey)
	provider.client = pointAt(t, srv)

	_, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		Network:    "ETH_MAINNET",
		WebhookURL: "https://hooks.example/alchemy",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, chain.ErrRateLimited)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), "dashboard.alchemy.com")
	assert.NotContains(t, err.Error(), "slow down")
	assert.NotContains(t, chain.CauseText(err), apiKey)
	assert.NotContains(t, chain.CauseText(err), "dashboard.alchemy.com")
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
