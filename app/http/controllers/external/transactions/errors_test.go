package transactions

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/services/withdraw"
)

// TestMapError_Show pins what a failed single-transaction read answers: only a
// transaction that is not the caller's is a 404; a failed store is a 500 that
// names no cause.
func TestMapError_Show(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"not found", withdraw.ErrTransactionNotFound, nethttp.StatusNotFound,
			`{"error":{"code":"not_found","message":"transaction not found"}}`},
		{"not found, wrapped", fmt.Errorf("get: %w", withdraw.ErrTransactionNotFound), nethttp.StatusNotFound,
			`{"error":{"code":"not_found","message":"transaction not found"}}`},
		{"a failed store", errors.New("dial tcp 10.0.0.7:5432: connection refused"), nethttp.StatusInternalServerError,
			`{"error":{"code":"internal_error","message":"internal_error"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(rec)
			ginCtx.Request = httptest.NewRequest(nethttp.MethodGet, "/", nil)

			if err := mapError(ginpkg.NewContext(ginCtx), tc.err, actionShow).Render(); err != nil {
				t.Fatal(err)
			}
			rec.Flush()

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Body.String(); got != tc.body {
				t.Errorf("body = %s, want %s", got, tc.body)
			}
		})
	}
}
