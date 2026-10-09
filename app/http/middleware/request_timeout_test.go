package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"
)

const (
	timeoutWireBody = `{"error":{"code":"timeout","message":"request timed out"}}`
	timeoutLimit    = 20 * time.Millisecond
)

// timeoutEngine puts the global chain's timeout, request id and security header
// links in front of one handler, wired as the gin driver wires a Goravel
// middleware.
func timeoutEngine(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, link := range []contractshttp.Middleware{RequestTimeout(timeoutLimit), RequestID(), SecurityHeaders()} {
		engine.Use(func(c *gin.Context) { link(ginpkg.NewContext(c)) })
	}
	engine.GET("/", handler)
	return engine
}

func TestRequest_Timeout_OnlyInstallsADeadline(t *testing.T) {
	var hasDeadline bool
	engine := timeoutEngine(func(c *gin.Context) {
		_, hasDeadline = c.Request.Context().Deadline()
		time.Sleep(3 * timeoutLimit)
		c.String(http.StatusOK, "late but whole")
	})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if !hasDeadline {
		t.Fatal("handler context has no deadline")
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "late but whole" {
		t.Fatalf("middleware cut the chain: %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestTimeout_Handler_Answers504WithTheWireTheMiddlewareSentBefore(t *testing.T) {
	engine := timeoutEngine(func(c *gin.Context) { time.Sleep(5 * timeoutLimit) })
	handler := TimeoutHandler(timeoutLimit, nil)(engine)

	cases := map[string]struct{ inbound, wantID string }{
		"generated id": {},
		"echoed id":    {inbound: "abc-1", wantID: "abc-1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.inbound != "" {
				request.Header.Set(requestIDHeader, tc.inbound)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusGatewayTimeout {
				t.Fatalf("status = %d", recorder.Code)
			}
			if got := recorder.Body.String(); got != timeoutWireBody {
				t.Fatalf("body = %q, want %q", got, timeoutWireBody)
			}
			header := recorder.Header()
			wantHeaders := map[string]string{
				"Content-Type":            "application/json; charset=utf-8",
				"Cache-Control":           "no-store",
				"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
				"Referrer-Policy":         "no-referrer",
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
			}
			for key, want := range wantHeaders {
				if got := header.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			id := header.Get(requestIDHeader)
			if !acceptableRequestID(id) || (tc.wantID != "" && id != tc.wantID) {
				t.Errorf("%s = %q", requestIDHeader, id)
			}
			if len(header) != len(wantHeaders)+1 {
				t.Errorf("headers = %v", header)
			}
		})
	}
}

func TestTimeout_Handler_FlushesAnAnswerThatBeatsTheDeadline(t *testing.T) {
	engine := timeoutEngine(func(c *gin.Context) {
		c.Header("X-Custom", "yes")
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})
	direct := httptest.NewRecorder()
	engine.ServeHTTP(direct, httptest.NewRequest(http.MethodGet, "/", nil))
	wrapped := httptest.NewRecorder()
	TimeoutHandler(time.Second, nil)(engine).ServeHTTP(wrapped, httptest.NewRequest(http.MethodGet, "/", nil))

	if wrapped.Code != http.StatusCreated || wrapped.Body.String() != direct.Body.String() {
		t.Fatalf("wrapped = %d %q, direct = %d %q", wrapped.Code, wrapped.Body.String(), direct.Code, direct.Body.String())
	}
	if wrapped.Header().Get("X-Custom") != "yes" || wrapped.Header().Get("Content-Type") != direct.Header().Get("Content-Type") {
		t.Fatalf("headers = %v", wrapped.Header())
	}
}

func TestTimeout_Handler_WithoutADeadlineIsTheHandlerItself(t *testing.T) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if got := TimeoutHandler(0, nil)(inner); fmt.Sprintf("%p", got) != fmt.Sprintf("%p", inner) {
		t.Fatal("a non-positive timeout must not wrap the handler")
	}
}

// Request A overruns the deadline and writes late; request B, served by the
// same engine and its pooled contexts, must never receive A's bytes. Channels
// order the events, so B is always in flight when A's handler writes.
func TestTimeout_Handler_LateWriteNeverReachesTheNextRequest(t *testing.T) {
	const limit = 100 * time.Millisecond
	type round struct{ release, bStarted, aWrote chan struct{} }
	var current atomic.Pointer[round]

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/a", func(c *gin.Context) {
		r := current.Load()
		<-r.release // outlives the deadline until the test lets it go
		c.String(http.StatusOK, "SECRET-OF-REQUEST-A")
		close(r.aWrote)
	})
	engine.GET("/b", func(c *gin.Context) {
		r := current.Load()
		close(r.bStarted)
		<-r.aWrote
		c.String(http.StatusOK, "answer-of-b")
	})
	server := httptest.NewServer(TimeoutHandler(limit, nil)(engine))
	defer server.Close()

	get := func(path string) (int, string) {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			return 0, ""
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	for i := range 10 {
		r := &round{release: make(chan struct{}), bStarted: make(chan struct{}), aWrote: make(chan struct{})}
		current.Store(r)
		if status, _ := get("/a"); status != http.StatusGatewayTimeout {
			t.Fatalf("iteration %d: A status = %d", i, status)
		}
		type answer struct {
			status int
			body   string
		}
		answered := make(chan answer, 1)
		go func() {
			status, body := get("/b")
			answered <- answer{status, body}
		}()
		<-r.bStarted
		close(r.release)
		got := <-answered
		if strings.Contains(got.body, "SECRET") || got.status != http.StatusOK || got.body != "answer-of-b" {
			t.Fatalf("iteration %d: B = %d %q", i, got.status, got.body)
		}
	}
}
