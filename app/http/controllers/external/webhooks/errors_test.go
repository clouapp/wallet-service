package webhooks

import (
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
)

// TestMap_Error_AnswersARefusedURLWithTheRule pins the bytes of a refused webhook URL
// on create and on an update that keeps it active.
func TestMap_Error_AnswersARefusedURLWithTheRule(t *testing.T) {
	refused := fmt.Errorf("%w: %w", webhook.ErrURLNotAllowed, &egress.Refusal{Reason: egress.ReasonPrivate})
	want := `{"error":{"code":"webhook_url_not_allowed","message":"` + egress.ReasonPrivate + `"}}` + "\n"
	for _, action := range []string{actionStore, actionUpdate} {
		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", nil)
		if err := mapError(ginpkg.NewContext(ginCtx), refused, action).Render(); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 422 || rec.Body.String() != want {
			t.Fatalf("%s: answer = %d %q, want 422 %q", action, rec.Code, rec.Body.String(), want)
		}
	}
}
