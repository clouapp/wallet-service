package webhooks

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
)

// TestMap_Error_AnswersARefusedURLWithTheRuleAndATestFailureWithoutTheReceiver pins
// the bytes of the wallet webhook refusals: a refused URL (on create, or a test of a
// webhook stored before the guard) is 422 with the rule, a failed test 502 with
// nothing of what the receiver answered.
func TestMap_Error_AnswersARefusedURLWithTheRuleAndATestFailureWithoutTheReceiver(t *testing.T) {
	refused := fmt.Errorf("%w: %w", webhook.ErrURLNotAllowed, &egress.Refusal{Reason: egress.ReasonLinkLocal})
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"a refused URL on create", refused, 422,
			`{"error":{"code":"webhook_url_not_allowed","message":"` + egress.ReasonLinkLocal + `"}}`},
		{"a refused URL on a test", fmt.Errorf("%w: %w", walletrecords.ErrWebhookTestFailed, refused), 422,
			`{"error":{"code":"webhook_url_not_allowed","message":"` + egress.ReasonLinkLocal + `"}}`},
		{"a test the receiver answered with an error", fmt.Errorf("%w: %w", walletrecords.ErrWebhookTestFailed,
			errors.New(`webhook test delivery failed: HTTP 500 {"AccessKeyId":"receiver-body"}`)), 502,
			`{"error":{"code":"provider_unavailable","message":"webhook test delivery failed"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(rec)
			ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", nil)
			if err := mapError(ginpkg.NewContext(ginCtx), tc.err, "send webhook test").Render(); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.status || rec.Body.String() != tc.body {
				t.Fatalf("answer = %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.body)
			}
		})
	}
}
