package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"

	contractshttp "github.com/goravel/framework/contracts/http"
)

const (
	requestIDHeader    = "X-Request-ID"
	maxRequestIDLength = 64
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type requestIDKey struct{}

// RequestID echoes a correlation id on every response. An inbound id is kept
// only when it is a short token; anything else is replaced so the header
// cannot carry free text.
func RequestID() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		id := ctx.Request().Header(requestIDHeader)
		if !acceptableRequestID(id) {
			id = newRequestID()
		}
		ctx.Response().Header(requestIDHeader, id)
		if origin := ctx.Request().Origin(); origin != nil {
			ctx.WithContext(context.WithValue(origin.Context(), requestIDKey{}, id))
		}
		ctx.Request().Next()
	}
}

// RequestIDFromContext returns the id RequestID stored, or "" when absent.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func acceptableRequestID(id string) bool {
	return len(id) <= maxRequestIDLength && requestIDPattern.MatchString(id)
}

func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "generated"
	}
	return hex.EncodeToString(buf)
}
