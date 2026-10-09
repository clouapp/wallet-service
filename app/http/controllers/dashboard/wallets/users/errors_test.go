package users

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
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
		{"roles outside the vocabulary", models.ErrInvalidWalletRoles, "add wallet user", 422, envelopeJSON, `{"error":{"code":"unprocessable","message":"roles must be a set of admin, spender, approver, viewer"}}` + "\n"},
		{"not an account member", walletrecords.ErrNotAccountMember, "add wallet user", 422, legacyJSON, `{"error":{"code":"unprocessable","message":"user is not an active member of this account"}}`},
		{"a failed membership read is unavailable and leaks no cause", fmt.Errorf("%w: %w", walletrecords.ErrMembershipLookup, errors.New("membership store unavailable")), "add wallet user", 503, legacyJSON, `{"error":{"code":"unavailable","message":"failed to load membership"}}`},
		{"a failed restore", fmt.Errorf("%w: %w", walletrecords.ErrMembershipRestore, errors.New("pq: down")), "add wallet user", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to restore wallet user"}}`},
		{"a failed create", errors.New("add wallet user: pq: down"), "add wallet user", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to add wallet user"}}`},
		{"a failed list", errors.New("pq: down"), "fetch wallet users", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to fetch wallet users"}}`},
		{"a failed removal", errors.New("pq: down"), "remove wallet user", 500, legacyJSON, `{"error":{"code":"internal","message":"failed to remove wallet user"}}`},
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
