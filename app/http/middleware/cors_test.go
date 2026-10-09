package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"
	"github.com/stretchr/testify/assert"
)

const (
	corsFront   = "http://localhost:2001"
	corsAllowed = "Content-Type, Authorization, X-Account-Id, X-API-Key, X-Timestamp, X-Signature"
	corsMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
)

// corsHeaders are the response headers the middleware may stamp.
var corsHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Methods",
	"Access-Control-Max-Age",
	"Vary",
}

// TestCors_Headers_AreTheOnesTheFrontEndRelyOn pins every header and the
// status of each CORS case (the origin list is parsed in config/cors.go): the project answers every OPTIONS with 204 and
// echoes the exact allowed origin with credentials, with a fixed allow-list of
// headers and methods. The gin driver's CORS (rs/cors) answers differently
// (it echoes the requested method and headers, adds Allow-Private-Network, and
// lets an OPTIONS without Access-Control-Request-Method fall through to the
// router), so this middleware stays; see cors.go.
func TestCors_Headers_AreTheOnesTheFrontEndRelyOn(t *testing.T) {
	allowedCase := map[string]string{
		"Access-Control-Allow-Origin":      corsFront,
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Headers":     corsAllowed,
		"Access-Control-Allow-Methods":     corsMethods,
		"Vary":                             "Origin",
	}
	cases := []struct {
		name        string
		origins     []string
		method      string
		request     map[string]string
		wantStatus  int
		wantNext    bool // false: the middleware ended the chain
		wantHeaders map[string]string
	}{
		{"same origin", []string{corsFront}, http.MethodGet, nil, http.StatusOK, true, nil},
		{"allowed origin", []string{corsFront}, http.MethodGet, map[string]string{"Origin": corsFront}, http.StatusOK, true, allowedCase},
		{"allowed origin POST", []string{corsFront}, http.MethodPost, map[string]string{"Origin": corsFront}, http.StatusOK, true, allowedCase},
		{"disallowed origin", []string{corsFront}, http.MethodGet, map[string]string{"Origin": "http://evil.example"}, http.StatusOK, true, nil},
		{"preflight", []string{corsFront}, http.MethodOptions, map[string]string{
			"Origin":                         corsFront,
			"Access-Control-Request-Method":  "PATCH",
			"Access-Control-Request-Headers": "authorization,x-account-id",
		}, http.StatusNoContent, false, allowedCase},
		{"options with origin, no request method", []string{corsFront}, http.MethodOptions, map[string]string{"Origin": corsFront}, http.StatusNoContent, false, allowedCase},
		{"options without origin", []string{corsFront}, http.MethodOptions, nil, http.StatusNoContent, false, nil},
		{"preflight from a disallowed origin", []string{corsFront}, http.MethodOptions, map[string]string{
			"Origin":                        "http://evil.example",
			"Access-Control-Request-Method": "GET",
		}, http.StatusNoContent, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder, gctx := corsContext(tc.method, tc.request)

			Cors(tc.origins)(ctx)

			assert.Equal(t, tc.wantStatus, recorder.Code)
			assert.Equal(t, tc.wantNext, !gctx.IsAborted())
			for _, name := range corsHeaders {
				assert.Equal(t, tc.wantHeaders[name], recorder.Header().Get(name), name)
			}
		})
	}
}

func corsContext(method string, headers map[string]string) (contractshttp.Context, *httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	gctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, "/v1/ping", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	gctx.Request = request
	return ginpkg.NewContext(gctx), recorder, gctx
}
