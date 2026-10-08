package responses

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	gincodecjson "github.com/gin-gonic/gin/codec/json"
	contractshttp "github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/http/resources"
)

func newContext(t *testing.T, request *http.Request) (contractshttp.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = request
	return ginpkg.NewContext(ginCtx), rec
}

func TestError_Writes_TheEnvelope(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))

	require.NoError(t, Error(ctx, http.StatusNotFound, resources.CodeNotFound, "not found").Render())
	rec.Flush()

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":{"code":"not_found","message":"not found"}}`, rec.Body.String())
}

func TestInternal_Error_HidesTheCause(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))

	require.NoError(t, InternalError(ctx, errors.New("dial tcp 10.0.0.7:5432: connect: refused")).Render())
	rec.Flush()

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"internal","message":"internal error"}}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "10.0.0.7")
}

func TestProvider_Error_IsBadGatewayWithoutTheUpstreamText(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodPost, "/", nil))

	require.NoError(t, ProviderError(ctx, errors.New("403 AccessDenied for key acme/logo.png")).Render())
	rec.Flush()

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"provider_unavailable","message":"provider unavailable"}}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "AccessDenied")
}

func TestField_Error_StaysUnprocessableWithTheFieldMap(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodPatch, "/", nil))

	require.NoError(t, FieldError(ctx, "status", "status cannot be set to active while suspended").Render())
	rec.Flush()

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"status":["status cannot be set to active while suspended"]}}`, rec.Body.String())
}

func TestField_Error_EmptyMessageNamesTheField(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodPatch, "/", nil))

	require.NoError(t, FieldError(ctx, "status", "").Render())
	rec.Flush()

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"status":["status is invalid"]}}`, rec.Body.String())
}

func TestValidation_Failed_StaysUnprocessable(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodPost, "/", nil))

	require.NoError(t, ValidationFailed(ctx, fakeErrors{all: map[string]map[string]string{
		"email": {"required": "Email address is required"},
	}}).Render())
	rec.Flush()

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"email":["Email address is required"]}}`, rec.Body.String())
}

func TestJSON_Encode_FailureAnswersTheEnvelope(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))

	unencodable := map[string]any{"ok": "value", "broken": make(chan int)}
	require.NoError(t, JSON(ctx, http.StatusOK, unencodable).Render())
	rec.Flush()

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"internal","message":"internal error"}}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "value")
}

func TestJSON_Matches_StdlibEncoder(t *testing.T) {
	payload := map[string]any{
		"zebra": 1,
		"alpha": 2,
		"mid":   3,
		"rtp":   96.6,
	}
	var want bytes.Buffer
	require.NoError(t, json.NewEncoder(&want).Encode(payload))

	ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NoError(t, JSON(ctx, http.StatusOK, payload).Render())

	assert.Equal(t, want.String(), rec.Body.String())
	assert.Equal(t, "encoding/json", gincodecjson.Package)
}

func TestSend_Success_BodyIsNotAnErrorEnvelope(t *testing.T) {
	ctx, rec := newContext(t, httptest.NewRequest(http.MethodGet, "/", nil))

	require.NoError(t, Send(ctx, http.StatusOK, contractshttp.Json{"status": "ok"}).Render())
	rec.Flush()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "error")
}
