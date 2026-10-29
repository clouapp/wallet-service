package bootstrap_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// A Helius delivery is a JSON array. The gin driver builds its request, which
// reads an application/json body, closes it and puts it back only when it decodes
// as an object, before any middleware of this application: served from the router
// alone, the array's body is closed when ProviderSignature reads it (400 invalid
// body: every Helius push was lost on production on 2026-10-29). The handler the
// servers run keeps it, and the request reaches the subscription lookup (none
// here: 404). Both are served over a real HTTP server, whose request body fails a
// read once closed, as production's does.
func TestHTTP_Handler_KeepsTheJSONArrayBodyOfAnInboundWebhook(t *testing.T) {
	fixtures.TestDB(t)
	post := func(handler http.Handler) (int, string) {
		server := httptest.NewServer(handler)
		defer server.Close()
		response, err := http.Post(server.URL+"/v1/webhooks/ingest/helius/sol", "application/json",
			strings.NewReader(`[{"signature":"sig","transactionError":null}]`))
		require.NoError(t, err)
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(body)
	}

	bareStatus, bareBody := post(appfacades.Route())
	servedStatus, servedBody := post(bootstrap.HTTPHandler(appfacades.Route()))

	assert.Equal(t, http.StatusBadRequest, bareStatus, "the router alone loses the array's body: %s", bareBody)
	assert.Contains(t, bareBody, "invalid body")
	assert.Equal(t, http.StatusNotFound, servedStatus, "the body reaches the subscription lookup: %s", servedBody)
	assert.Contains(t, servedBody, "webhook subscription not found")
}
