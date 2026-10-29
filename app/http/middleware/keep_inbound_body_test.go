package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeepInboundBody_MarksAProviderWebhookBodyAsOctetStreamAndLeavesItWhole(t *testing.T) {
	var contentType, body string
	handler := KeepInboundBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body = string(raw)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/ingest/helius/sol", strings.NewReader(`[{"signature":"sig"}]`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	assert.Equal(t, "application/octet-stream", contentType)
	assert.Equal(t, `[{"signature":"sig"}]`, body)
}

func TestKeepInboundBody_LeavesEveryOtherRequestAlone(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/auth/login"},
		{http.MethodPost, "/api/v1/webhooks"},
		{http.MethodGet, "/v1/webhooks/ingest/helius/sol"},
	} {
		var contentType string
		handler := KeepInboundBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			contentType = r.Header.Get("Content-Type")
		}))
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")

		handler.ServeHTTP(httptest.NewRecorder(), request)

		assert.Equal(t, "application/json", contentType, "%s %s", tc.method, tc.path)
	}
}
