package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/http/resources"
)

const timeoutMessage = "request timed out"

// TimeoutHandler cuts a request off at timeout, for the net/http server
// (runLocal). The inner handler runs on its own goroutine against a private
// buffer, never the connection: after the deadline its late writes land in that
// buffer and are dropped, so they cannot reach another request through a pooled
// gin context. On the deadline the client gets the 504 envelope, with the
// headers the global chain stamps on every answer.
//
// The answer is buffered whole before it is sent. A non-positive timeout returns
// next unchanged.
func TimeoutHandler(timeout time.Duration) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			// RequestID keeps an acceptable inbound id, so fixing it here makes
			// the id on a 504 the one the chain and its logs use.
			requestID := requestIDFor(r.Header.Get(requestIDHeader))
			r.Header.Set(requestIDHeader, requestID)

			buffer := &bufferedResponse{header: http.Header{}}
			done := make(chan struct{})
			panicked := make(chan any, 1)
			go func() {
				defer close(done)
				defer func() {
					if recovered := recover(); recovered != nil {
						panicked <- recovered
					}
				}()
				next.ServeHTTP(buffer, r.WithContext(ctx))
			}()

			select {
			case recovered := <-panicked:
				panic(recovered)
			case <-done:
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					buffer.discard()
					writeTimeout(w, r, requestID)
					return
				}
				<-done // the client went away; let the handler finish
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				writeTimeout(w, r, requestID)
				return
			}
			buffer.flushTo(w)
		})
	}
}

// timeoutBody is the strict envelope the 504 has always carried.
var timeoutBody = func() []byte {
	body, err := json.Marshal(resources.NewError(resources.ErrorDeps{Code: resources.CodeTimeout, Message: timeoutMessage}))
	if err != nil {
		panic(err)
	}
	return body
}()

func writeTimeout(w http.ResponseWriter, r *http.Request, requestID string) {
	header := w.Header()
	set := func(key, value string) { header.Set(key, value) }
	setSecurityHeaders(set, r.TLS != nil)
	setCorsHeaders(set, r.Header.Get("Origin"))
	header.Set(requestIDHeader, requestID)
	header.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusGatewayTimeout)
	_, _ = w.Write(timeoutBody)
}

// bufferedResponse is the http.ResponseWriter the inner handler sees. Only its
// own goroutine touches header; discard and flushTo run when that goroutine is
// finished or abandoned, and the mutex orders the abandoned case.
type bufferedResponse struct {
	header    http.Header
	mu        sync.Mutex
	body      bytes.Buffer
	status    int
	discarded bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.discarded {
		return len(p), nil
	}
	return b.body.Write(p)
}

func (b *bufferedResponse) discard() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.discarded = true
	b.body.Reset()
}

func (b *bufferedResponse) flushTo(w http.ResponseWriter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, values := range b.header {
		w.Header()[key] = values
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(b.body.Bytes())
}
