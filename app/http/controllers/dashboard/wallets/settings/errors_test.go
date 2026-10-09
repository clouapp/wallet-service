package settings

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	settingsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/settings"
	"github.com/macrowallets/waas/app/services/walletsettings"
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
		{"no body", settingsrequests.ErrBodyRequired, "update wallet settings", 400, envelopeJSON, `{"error":{"code":"invalid_request","message":"request body is required"}}` + "\n"},
		{"an unreadable body", settingsrequests.ErrBodyUnreadable, "update wallet settings", 400, envelopeJSON, `{"error":{"code":"invalid_request","message":"request body could not be read"}}` + "\n"},
		{"nothing to change", walletsettings.ErrNoFields, "update wallet settings", 400, envelopeJSON, `{"error":{"code":"invalid_request","message":"no settings to update"}}` + "\n"},
		{"a rejected field", &walletsettings.FieldError{Field: "label", Message: "must not be empty"}, "update wallet settings", 422, legacyJSON, `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"label":["must not be empty"]}}`},
		{"a fee change on an unreadable chain", walletsettings.ErrChainNotFound, "update wallet settings", 422, legacyJSON, `{"error":{"code":"unprocessable","message":"chain not found"}}`},
		{"an archived wallet again", walletsettings.ErrAlreadyArchived, "archive wallet", 409, legacyJSON, `{"error":{"code":"conflict","message":"wallet already archived"}}`},
		{"a failed update", errors.New("update wallet settings: pq: down"), "update wallet settings", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to update wallet settings"}}`},
		{"a failed archive", errors.New("pq: down"), "archive wallet", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to archive wallet"}}`},
		{"a failed freeze", fmt.Errorf("freeze wallet: %w", errors.New("pq: down")), "freeze wallet", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to freeze wallet"}}`},
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
