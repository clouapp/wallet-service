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
	chainsvc "github.com/macrowallets/waas/app/services/chains"
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
		action      string
		status      int
		contentType string
		body        string
	}{
		{"no account", walletview.ErrAccountRequired, "fetch wallets", 400, legacyJSON, `{"error":{"code":"invalid_request","message":"account is required"}}`},
		{"no user", walletview.ErrViewerRequired, "fetch wallets", 401, legacyJSON, `{"error":{"code":"unauthorized","message":"unauthorized"}}`},
		{"not the caller's wallet", walletview.ErrWalletNotFound, "fetch wallet", 404, legacyJSON, `{"error":{"code":"not_found","message":"wallet not found"}}`},
		{"a failed page read", &walletview.FetchError{What: "wallets", Err: errors.New("pq: down")}, "fetch wallets", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to fetch wallets"}}`},
		{"failed balances", &walletview.FetchError{What: "wallet balances", Err: errors.New("pq: down")}, "fetch wallets", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to fetch wallet balances"}}`},
		{"a failed membership read", &walletview.FetchError{What: "wallet", Err: errors.New("pq: down")}, "fetch wallet", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to fetch wallet"}}`},
		{"a chain of the other network kind", chainsvc.ErrChainNotInEnvironment, "create wallet", 403, legacyJSON, `{"error":{"code":"forbidden","message":"chain not available in current environment"}}`},
		{"an unknown chain names no chain", fmt.Errorf("%w: sol", chainregistry.ErrUnknownChain), "create wallet", 400, envelopeJSON, `{"error":{"code":"invalid_request","message":"unknown chain"}}` + "\n"},
		{"activation: no such wallet", wallet.ErrWalletNotFound, actionActivate, 404, envelopeJSON, `{"error":{"code":"not_found","message":"wallet not found"}}` + "\n"},
		{"activation: already active", wallet.ErrWalletAlreadyActive, actionActivate, 409, envelopeJSON, `{"error":{"code":"conflict","message":"wallet is not pending activation"}}` + "\n"},
		{"activation: wrong code", wallet.ErrInvalidActivationCode, actionActivate, 400, envelopeJSON, `{"error":{"code":"invalid_request","message":"invalid activation code"}}` + "\n"},
		{"creation outage hides the cause", errors.New("create wallet: pq: connection refused at db.internal"), "create wallet", 500, envelopeJSON, `{"error":{"code":"internal","message":"internal error"}}` + "\n"},
		{"activation outage keeps the legacy writer", errors.New("pq: down"), actionActivate, 500, legacyJSON, `{"error":{"code":"internal","message":"internal error"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, contentType, body := answer(t, func(ctx http.Context) http.Response { return mapError(ctx, tc.err, tc.action) })

			if status != tc.status || contentType != tc.contentType || body != tc.body {
				t.Fatalf("answered %d %q %q\nwant     %d %q %q", status, contentType, body, tc.status, tc.contentType, tc.body)
			}
		})
	}
}
