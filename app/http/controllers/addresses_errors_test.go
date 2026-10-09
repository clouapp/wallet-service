package controllers

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletops"
)

// TestAddressErrors_ClassifyBySentinel pins the status and body of the address
// error mappers. The wallet service signals each client-facing refusal with an
// exported sentinel; the mappers match it with errors.Is, never by the text.
func TestAddressErrors_ClassifyBySentinel(t *testing.T) {
	const (
		unprocessable = `{"error":{"code":"unprocessable","message":"%s"}}` + "\n"
		notFound      = `{"error":{"code":"not_found","message":"%s"}}` + "\n"
		internal      = `{"error":{"code":"internal","message":"internal error"}}` + "\n"
		providerDown  = `{"error":{"code":"provider_unavailable","message":"provider unavailable"}}` + "\n"
		// Fail writes without the trailing newline Error writes.
		noFields   = `{"error":{"code":"invalid_request","message":"no fields to update"}}`
		listFailed = `{"error":{"code":"internal","message":"failed to fetch addresses"}}`
	)
	cases := []struct {
		name   string
		answer func(http.Context, error, string) http.Response
		err    error
		status int
		body   string
	}{
		{"generate: wallet not found", mapAddressError, wallet.ErrWalletNotFound, 422, fmt.Sprintf(unprocessable, "wallet not found")},
		{"generate: ed25519 passphrase required", mapAddressError, wallet.ErrAddressPassphraseRequired, 422, fmt.Sprintf(unprocessable, "passphrase is required for ed25519 address derivation")},
		{"generate: invalid passphrase", mapAddressError, wallet.ErrInvalidPassphrase, 422, fmt.Sprintf(unprocessable, "invalid passphrase")},
		{"generate: a wrapped store failure", mapAddressError, fmt.Errorf("create address: %w", errors.New("pq: boom")), 500, internal},
		{"generate: the same words without the sentinel", mapAddressError, errors.New("wallet not found"), 500, internal},
		{"update: address not found", mapAddressError, wallet.ErrAddressNotFound, 404, fmt.Sprintf(notFound, "address not found")},
		{"update: label type", mapAddressError, wallet.ErrAddressLabelNotString, 404, fmt.Sprintf(notFound, "update address: label must be a string")},
		{"update: external user id type", mapAddressError, wallet.ErrAddressExternalUserIDNotString, 404, fmt.Sprintf(notFound, "update address: external_user_id must be a string")},
		{"update: a wrapped store failure", mapAddressError, fmt.Errorf("update address: %w", errors.New("pq: boom")), 500, internal},
		{"update: the same words without the sentinel", mapAddressError, errors.New("address not found"), 500, internal},
		{"generate: an upstream provider failure names no provider text", mapAddressError,
			fmt.Errorf("fetch service share: %w", stubProviderError{text: "api error AccessDeniedException: not authorized for arn:aws:secretsmanager:us-east-1:0:secret:share-b-AbCdEf"}), 502, providerDown},
		{"update: nothing to change", mapAddressError, walletops.ErrNoFields, 400, noFields},
		{"list: the page could not be read", mapAddressError, fmt.Errorf("%w: %w", walletops.ErrAddressesUnavailable, errors.New("pq: boom")), 500, listFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(rec)
			ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", nil)
			response := tc.answer(ginpkg.NewContext(ginCtx), tc.err, "address action")
			if response == nil {
				t.Fatal("no response")
			}
			if err := response.Render(); err != nil {
				t.Fatalf("render: %v", err)
			}
			rec.Flush()
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Body.String(); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

type stubProviderError struct{ text string }

func (e stubProviderError) Error() string     { return e.text }
func (e stubProviderError) ErrorCode() string { return "AccessDeniedException" }
