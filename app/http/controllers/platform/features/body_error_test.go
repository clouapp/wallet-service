package features

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	featuresrequests "github.com/macrowallets/waas/app/http/requests/platform/features"
)

// TestPlatform_Feature_BodyErrorKeepsItsBytes pins the 422 a scoped write
// without "enabled" answers: the validation envelope through the legacy
// writer, with its one field message.
func TestPlatform_Feature_BodyErrorKeepsItsBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(nethttp.MethodPatch, "/", nil)

	if err := mapError(ginpkg.NewContext(ginCtx), featuresrequests.ErrEnabledRequired, "set platform feature").Render(); err != nil {
		t.Fatal(err)
	}
	rec.Flush()

	if rec.Code != nethttp.StatusUnprocessableEntity {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	const want = `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"enabled":["enabled is required"]}}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %s", got)
	}
}
