package currencies

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	currencysvc "github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/price"
)

// TestConvert_Failure_ClassifiesBySentinel pins the status and body of every
// convert failure. The price service marks each one with an exported sentinel
// and the handler matches it with errors.Is, never by the text.
func TestConvert_Failure_ClassifiesBySentinel(t *testing.T) {
	const (
		provider = `{"error":{"code":"provider_unavailable","message":"provider unavailable"}}` + "\n"
		missing  = `{"error":{"code":"invalid_request","message":"currency not found"}}` + "\n"
		required = `{"error":{"code":"invalid_request","message":"from, to, and amount are required"}}` + "\n"
		internal = `{"error":{"code":"internal","message":"internal error"}}` + "\n"
		// Fail writes without the trailing newline Error writes.
		amount       = `{"error":{"code":"invalid_request","message":"amount must be a positive number"}}`
		requiredFail = `{"error":{"code":"invalid_request","message":"from, to, and amount are required"}}`
		notFound     = `{"error":{"code":"not_found","message":"currency not found"}}`
		readFailed   = `{"error":{"code":"internal","message":"failed to convert currency"}}`
	)
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"never quoted", fmt.Errorf("price for XYZ: %w: XYZ", price.ErrPriceNotQuoted), 502, provider},
		{"zero price", fmt.Errorf("%w XYZ", price.ErrZeroPrice), 502, provider},
		{"unknown currency, wrapped by Convert", fmt.Errorf("price for XYZ: %w: XYZ", price.ErrCurrencyNotFound), 400, missing},
		{"missing codes", price.ErrCurrencyCodesRequired, 400, required},
		{"missing code from GetPrice", fmt.Errorf("price for : %w", price.ErrCurrencyCodeRequired), 400, required},
		{"a store failure", fmt.Errorf("%w: %w", price.ErrQuoteFailed, errors.New("pq: connection refused")), 500, internal},
		{"the amount is not a positive number", price.ErrQuoteAmountInvalid, 400, amount},
		{"a fixed code or amount is missing", price.ErrQuoteFieldsRequired, 400, requiredFail},
		{"an unknown currency code on the catalogue", currencysvc.ErrNotFound, 404, notFound},
		{"the same words without the sentinel", errors.New("currency not found: XYZ"), 500, readFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(rec)
			ginCtx.Request = httptest.NewRequest(nethttp.MethodGet, "/", nil)
			response := mapError(ginpkg.NewContext(ginCtx), tc.err, "convert currency")
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
