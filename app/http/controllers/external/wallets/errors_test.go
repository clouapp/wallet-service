package wallets

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletview"
)

// legacyJSON is responses.Fail's writer (gin, no trailing newline); envelopeJSON
// is responses.Error's (encoding/json encoder, trailing newline).
const (
	legacyJSON   = "application/json; charset=utf-8"
	envelopeJSON = "application/json"
)

func answer(t *testing.T, respond func(http.Context) http.Response) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", nil)
	response := respond(ginpkg.NewContext(ginCtx))
	if response == nil {
		t.Fatal("no response")
	}
	if err := response.Render(); err != nil {
		t.Fatalf("render: %v", err)
	}
	rec.Flush()
	return rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()
}

func TestMapError_KeepsItsBytes(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		status      int
		contentType string
		body        string
	}{
		{"an unknown chain names no chain", fmt.Errorf("%w: sol", chainregistry.ErrUnknownChain), 409, legacyJSON, `{"error":{"code":"conflict","message":"unknown chain"}}`},
		{"a short passphrase", wallet.ErrPassphraseTooShort, 422, legacyJSON, `{"error":{"code":"unprocessable","message":"passphrase must be at least 12 characters"}}`},
		{"a creation without an account", wallet.ErrAccountRequired, 400, legacyJSON, `{"error":{"code":"invalid_request","message":"account_id is required"}}`},
		{"a read without an account", walletview.ErrAccountRequired, 400, legacyJSON, `{"error":{"code":"invalid_request","message":"account is required"}}`},
		{"a wallet that is not the caller's", walletview.ErrWalletNotFound, 404, legacyJSON, `{"error":{"code":"not_found","message":"wallet not found"}}`},
		{"a failed read", &walletview.FetchError{What: "wallets", Err: errors.New("pq: down")}, 500, legacyJSON, `{"error":{"code":"internal","message":"failed to fetch wallets"}}`},
		{"an outage is not hidden as 409 and leaks no cause", errors.New("create wallet: pq: connection refused at db.internal"), 500, envelopeJSON, `{"error":{"code":"internal","message":"internal error"}}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, contentType, body := answer(t, func(ctx http.Context) http.Response { return mapError(ctx, tc.err, "create wallet") })

			if status != tc.status || contentType != tc.contentType || body != tc.body {
				t.Fatalf("answered %d %q %q\nwant     %d %q %q", status, contentType, body, tc.status, tc.contentType, tc.body)
			}
		})
	}
}
