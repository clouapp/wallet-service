package responses

import (
	"net/http"
	"net/http/httptest"
	"testing"

	contractshttp "github.com/goravel/framework/contracts/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFail_Writers_KeepTheLegacyBytes pins the writers the legacy error maps
// move onto: ctx.Response().Json, so the content type keeps its charset and
// the body has no trailing newline, with the extra contract fields inside the
// error object and keys in encoding/json order.
func TestFail_Writers_KeepTheLegacyBytes(t *testing.T) {
	cases := []struct {
		name   string
		answer func(contractshttp.Context) contractshttp.AbortableResponse
		status int
		body   string
	}{
		{"Fail", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return Fail(ctx, http.StatusNotFound, CodeNotFound, "wallet not found")
		}, http.StatusNotFound, `{"error":{"code":"not_found","message":"wallet not found"}}`},
		{"FailWith", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return FailWith(ctx, http.StatusTooManyRequests, CodeSweepLimitExceeded, "sweep_limit_exceeded",
				map[string]any{"limit_type": "in_flight_consolidation", "retry_after_seconds": 60})
		}, http.StatusTooManyRequests, `{"error":{"code":"sweep_limit_exceeded","limit_type":"in_flight_consolidation","message":"sweep_limit_exceeded","retry_after_seconds":60}}`},
		{"FailWith keeps code and message", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return FailWith(ctx, http.StatusForbidden, CodeAccountFrozen, "account is frozen; only reads are allowed",
				map[string]any{"status": "frozen", "code": "ignored", "message": "ignored"})
		}, http.StatusForbidden, `{"error":{"code":"account_frozen","message":"account is frozen; only reads are allowed","status":"frozen"}}`},
		{"FailMessage sentence", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return FailMessage(ctx, http.StatusInternalServerError, "failed to create token")
		}, http.StatusInternalServerError, `{"error":{"code":"internal","message":"failed to create token"}}`},
		{"FailMessage machine code", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return FailMessage(ctx, http.StatusInternalServerError, "internal_error")
		}, http.StatusInternalServerError, `{"error":{"code":"internal_error","message":"internal_error"}}`},
		{"FailMessage signature sentence", func(ctx contractshttp.Context) contractshttp.AbortableResponse {
			return FailMessage(ctx, http.StatusUnauthorized, "invalid webhook signature")
		}, http.StatusUnauthorized, `{"error":{"code":"invalid_signature","message":"invalid webhook signature"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))
			require.NoError(t, tc.answer(ctx).Render())
			rec.Flush()

			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Equal(t, tc.body, rec.Body.String())
		})
	}
}

// TestCode_For_IsTheLegacyHeuristic pins the code a message known only at
// run time gets: a listed sentence, a machine code, or the status default.
func TestCode_For_IsTheLegacyHeuristic(t *testing.T) {
	cases := []struct {
		status  int
		message string
		want    string
	}{
		{http.StatusUnauthorized, "missing request signature", CodeInvalidSignature},
		{http.StatusUnauthorized, "invalid request signature", CodeInvalidSignature},
		{http.StatusUnauthorized, "invalid webhook signature", CodeInvalidSignature},
		{http.StatusUnprocessableEntity, "wallet_not_gas_ready", "wallet_not_gas_ready"},
		{http.StatusForbidden, "forbidden", CodeForbidden},
		{http.StatusBadRequest, "invalid wallet id", CodeInvalidRequest},
		{http.StatusUnauthorized, "token expired", CodeUnauthorized},
		{http.StatusForbidden, "only owners and account admins may freeze wallets", CodeForbidden},
		{http.StatusNotFound, "Wallet not found", CodeNotFound},
		{http.StatusConflict, "wallet already archived", CodeConflict},
		{http.StatusRequestEntityTooLarge, "request body too large", CodeRequestTooLarge},
		{http.StatusUnprocessableEntity, "unknown asset", CodeUnprocessable},
		{http.StatusTooManyRequests, "slow down", CodeTooManyRequests},
		{http.StatusBadGateway, "webhook test delivery failed", CodeProviderUnavailable},
		{http.StatusServiceUnavailable, "failed to load membership", CodeUnavailable},
		{http.StatusGatewayTimeout, "request timed out", CodeTimeout},
		{http.StatusInternalServerError, "failed to fetch wallet", CodeInternal},
		{http.StatusTeapot, "short and stout", CodeInternal},
	}
	for _, tc := range cases {
		if got := CodeFor(tc.status, tc.message); got != tc.want {
			t.Errorf("CodeFor(%d, %q) = %q, want %q", tc.status, tc.message, got, tc.want)
		}
	}
}
