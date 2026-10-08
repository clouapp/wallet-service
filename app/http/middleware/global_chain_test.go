package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

func TestGlobal_Chain_FollowsThePlanOrder(t *testing.T) {
	links := globalChainLinks(time.Second, InboundSignatureDeps{Subscriptions: &ingestsvc.Subscriptions{}})
	want := []string{
		chainRequestTimeout,
		chainProviderSignature,
		chainRequestID,
		chainSecurityHeaders,
		chainBodyLimit,
		chainCORS,
	}
	if len(links) != len(want) {
		t.Fatalf("chain len = %d, want %d", len(links), len(want))
	}
	for i := range want {
		if links[i].name != want[i] {
			t.Fatalf("position %d = %q, want %q", i, links[i].name, want[i])
		}
		if links[i].middleware == nil {
			t.Fatalf("position %d (%s) has no middleware", i, want[i])
		}
	}
	if len(GlobalChain(time.Second, InboundSignatureDeps{Subscriptions: &ingestsvc.Subscriptions{}})) != len(want) {
		t.Fatalf("GlobalChain len = %d, want %d", len(GlobalChain(time.Second, InboundSignatureDeps{Subscriptions: &ingestsvc.Subscriptions{}})), len(want))
	}
}

func TestRequest_ID_ReplacesAFreeTextInboundID(t *testing.T) {
	ctx, recorder := newChainContext(http.MethodGet, nil, 0)
	ctx.Request().Origin().Header.Set("X-Request-ID", "not a token")
	RequestID()(ctx)
	got := recorder.Header().Get("X-Request-ID")
	if got == "" || got == "not a token" || strings.Contains(got, " ") {
		t.Fatalf("request id = %q, want a generated token", got)
	}
	if RequestIDFromContext(ctx.Request().Origin().Context()) != got {
		t.Fatalf("context id = %q, response id = %q", RequestIDFromContext(ctx.Request().Origin().Context()), got)
	}
}

func TestRequest_ID_KeepsAToken(t *testing.T) {
	ctx, recorder := newChainContext(http.MethodGet, nil, 0)
	ctx.Request().Origin().Header.Set("X-Request-ID", "trace-1")
	RequestID()(ctx)
	if got := recorder.Header().Get("X-Request-ID"); got != "trace-1" {
		t.Fatalf("request id = %q, want trace-1", got)
	}
}

func TestSecurity_Headers_AreSetBeforeNext(t *testing.T) {
	ctx, recorder := newChainContext(http.MethodGet, nil, 0)
	SecurityHeaders()(ctx)
	header := recorder.Header()
	if header.Get("X-Content-Type-Options") != headerContentTypeOptions {
		t.Fatalf("nosniff = %q", header.Get("X-Content-Type-Options"))
	}
	if header.Get("X-Frame-Options") != headerFrameOptions {
		t.Fatalf("frame = %q", header.Get("X-Frame-Options"))
	}
	if header.Get("Content-Security-Policy") != headerCSP {
		t.Fatalf("csp = %q", header.Get("Content-Security-Policy"))
	}
	if header.Get("Cache-Control") != headerCacheControl {
		t.Fatalf("cache = %q", header.Get("Cache-Control"))
	}
	if header.Get("Strict-Transport-Security") != "" {
		t.Fatal("plaintext connection must not send HSTS")
	}
}

func TestBody_Limit_RefusesADeclaredLengthAboveTheCeiling(t *testing.T) {
	ctx, recorder := newChainContext(http.MethodPost, strings.NewReader("x"), 2)
	BodyLimit(1)(ctx)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"code":"request_too_large"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestRecover_Panic_OmitsTheRequestFromTheBody(t *testing.T) {
	ctx, recorder := newChainContext(http.MethodPost, nil, 0)
	ctx.Request().Origin().Header.Set("Authorization", "Bearer secret-token")
	RecoverPanic(ctx, "boom")
	body := recorder.Body.String()
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %s", recorder.Code, body)
	}
	if strings.Contains(body, "secret-token") || strings.Contains(body, "Authorization") {
		t.Fatalf("body leaked the request: %s", body)
	}
	if !strings.Contains(body, `"code":"internal"`) {
		t.Fatalf("body = %s", body)
	}
}

func newChainContext(method string, body io.Reader, contentLength int64) (contractshttp.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	gctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, "/v1/ping", body)
	request.ContentLength = contentLength
	gctx.Request = request
	return ginpkg.NewContext(gctx), recorder
}
