package quicknode

import (
	"context"
	"encoding/base64"
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
		token  string
		raw    []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		token = r.Header.Get("x-api-key")
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"stream-1"}`))
	}))
	t.Cleanup(srv.Close)

	provider := NewQuickNodeProvider(apiKey)
	provider.client = pointAt(t, srv)

	got, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		ChainID:    "btc",
		Network:    "bitcoin-mainnet",
		WebhookURL: "https://hooks.example/quicknode",
		Addresses:  []string{"bc1qexample"},
		AuthSecret: "signing-secret",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "stream-1", got.ProviderWebhookID)
	assert.Equal(t, "signing-secret", got.SigningSecret)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/streams/rest/v1/streams", path)
	assert.Equal(t, apiKey, token)

	var body quicknodeCreateStreamReq
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, "BTC Deposit Monitor", body.Name)
	assert.Equal(t, "bitcoin-mainnet", body.Network)
	assert.Equal(t, "block", body.Dataset)
	assert.Equal(t, "active", body.Status)
	assert.Equal(t, "https://hooks.example/quicknode", body.Destination.URL)
	filter, decErr := base64.StdEncoding.DecodeString(body.FilterFunction)
	require.NoError(t, decErr)
	assert.Contains(t, string(filter), "bc1qexample")
}

func TestCreateWebhook_MapsHTTPErrorsWithoutTheKeyOrHost(t *testing.T) {
	const apiKey = "test-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(srv.Close)

	provider := NewQuickNodeProvider(apiKey)
	provider.client = pointAt(t, srv)

	_, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		WebhookURL: "https://hooks.example/quicknode",
		Addresses:  []string{"bc1qexample"},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, chain.ErrRateLimited)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), "api.quicknode.com")
	assert.NotContains(t, err.Error(), "slow down")
	assert.NotContains(t, chain.CauseText(err), apiKey)
	assert.NotContains(t, chain.CauseText(err), "api.quicknode.com")
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
