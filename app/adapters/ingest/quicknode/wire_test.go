package quicknode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		token = r.Header.Get("x-api-key")
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"stream-1","destination":"webhook","destination_attributes":{"url":"https://hooks.example/quicknode","security_token":"qn-token-1"}}`))
	}))
	t.Cleanup(srv.Close)

	provider := NewQuickNodeProvider(apiKey)
	provider.client = pointAt(t, srv)

	got, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{
		ChainID:    "btc",
		Network:    "bitcoin-mainnet",
		WebhookURL: "https://hooks.example/quicknode",
		Addresses:  []string{"bc1qexample"},
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "stream-1", got.ProviderWebhookID)
	assert.Equal(t, "qn-token-1", got.SigningSecret, "the stream's security token signs its deliveries")
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/streams/rest/v1/streams", path)
	assert.Equal(t, apiKey, token)

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	for _, field := range []string{"name", "network", "dataset", "filter_function", "region", "dataset_batch_size", "elastic_batch_enabled", "destination", "destination_attributes", "status"} {
		assert.Contains(t, body, field, "CreateStreamDto requires %s", field)
	}
	assert.True(t, strings.HasPrefix(body["name"].(string), "BTC Deposit Monitor "), "a name of its own, to find the stream by")
	assert.Equal(t, "bitcoin-mainnet", body["network"])
	assert.Equal(t, "block", body["dataset"])
	assert.Equal(t, "usa_east", body["region"])
	assert.Equal(t, float64(1), body["dataset_batch_size"])
	assert.Equal(t, false, body["elastic_batch_enabled"])
	assert.Equal(t, "paused", body["status"], "the stream pushes once its subscription is stored")
	assert.Equal(t, "webhook", body["destination"])
	attributes := body["destination_attributes"].(map[string]any)
	for _, field := range []string{"url", "security_token", "compression", "headers", "max_retry", "retry_interval_sec", "post_timeout_sec"} {
		assert.Contains(t, attributes, field, "WebhookAttributes requires %s", field)
	}
	assert.Equal(t, "https://hooks.example/quicknode", attributes["url"])
	assert.Equal(t, "none", attributes["compression"], "the signature covers the uncompressed body")
	assert.Equal(t, map[string]any{}, attributes["headers"])
	assert.Len(t, attributes["security_token"], 64, "a token of 32 random bytes")
	filter, decErr := base64.StdEncoding.DecodeString(body["filter_function"].(string))
	require.NoError(t, decErr)
	assert.Contains(t, string(filter), "bc1qexample")
}

// sentToken is the security token a create request carried.
func sentToken(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		DestinationAttributes struct {
			SecurityToken string `json:"security_token"`
		} `json:"destination_attributes"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.DestinationAttributes.SecurityToken
}

func TestCreate_Webhook_KeepsTheTokenItSentWhenTheAnswerHasNone(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"stream-1","destination_attributes":{"url":"https://hooks.example/quicknode"}}`))
	}))
	t.Cleanup(srv.Close)
	provider := NewQuickNodeProvider("test-key")
	provider.client = pointAt(t, srv)

	got, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{WebhookURL: "https://hooks.example/quicknode"})
	require.NoError(t, err)
	assert.Equal(t, "stream-1", got.ProviderWebhookID)
	assert.Equal(t, sentToken(t, raw), got.SigningSecret)
}

func TestCreate_Webhook_DeletesAStreamWhoseAnswerCannotBeRead(t *testing.T) {
	var created string
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/streams/rest/v1/streams":
			raw, _ := io.ReadAll(r.Body)
			var body struct{ Name string }
			_ = json.Unmarshal(raw, &body)
			created = body.Name
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`<html>created</html>`))
		case r.Method == http.MethodGet && r.URL.Path == "/streams/rest/v1/streams":
			_, _ = w.Write([]byte(`{"data":[{"id":"other","name":"Other"},{"id":"stream-9","name":"` + created + `"}]}`))
		case r.Method == http.MethodDelete:
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/streams/rest/v1/streams/"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	provider := NewQuickNodeProvider("test-key")
	provider.client = pointAt(t, srv)

	_, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{WebhookURL: "https://hooks.example/quicknode"})

	require.Error(t, err)
	assert.Equal(t, []string{"stream-9"}, deleted, "a stream nothing verifies is not left pushing")
}

func TestCreate_Webhook_NamesAStreamItCouldNotFindToDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	provider := NewQuickNodeProvider("test-key")
	provider.client = pointAt(t, srv)

	_, err := provider.CreateWebhook(context.Background(), providers.ProviderConfig{WebhookURL: "https://hooks.example/quicknode"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "BTC Deposit Monitor ", "the operator can find it by name")
}

func TestActivate_Webhook_PostsTheActivation(t *testing.T) {
	var method, path, token string
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, token = r.Method, r.URL.Path, r.Header.Get("x-api-key")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	provider := NewQuickNodeProvider("test-key")
	provider.client = pointAt(t, srv)

	require.NoError(t, provider.ActivateWebhook(context.Background(), "stream-1"))
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/streams/rest/v1/streams/stream-1/activate", path)
	assert.Equal(t, "test-key", token)

	status = http.StatusBadRequest
	assert.Error(t, provider.ActivateWebhook(context.Background(), "stream-1"))
	assert.Error(t, provider.ActivateWebhook(context.Background(), " "))
}

func TestCreate_Webhook_MapsHTTPErrorsWithoutTheKeyOrHost(t *testing.T) {
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
