package settings

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	settingssvc "github.com/macrowallets/waas/app/services/settings"
)

// TestSettings_ValidationError_KeepsItsBytes pins the 422 an invalid account
// settings write answers: the validation envelope through the legacy writer,
// with the service's field messages.
func TestSettings_ValidationError_KeepsItsBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(nethttp.MethodPatch, "/", nil)

	invalid := &settingssvc.ValidationError{Fields: map[string][]string{
		"session_idle_minutes": {"must be between 5 and 1440"},
		"zzz":                  {"unknown settings key"},
	}}
	if err := mapError(ginpkg.NewContext(ginCtx), invalid, "internal_error").Render(); err != nil {
		t.Fatal(err)
	}
	rec.Flush()

	if rec.Code != nethttp.StatusUnprocessableEntity {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	const want = `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"session_idle_minutes":["must be between 5 and 1440"],"zzz":["unknown settings key"]}}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %s", got)
	}
}
