package middleware

import (
	"net/http"
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// MaxGlobalBodyBytes is the request-body ceiling for every verb that carries
// a body. 4096 KiB is the gin driver's body_limit default, so the chain does
// not tighten a request the driver already accepted.
const MaxGlobalBodyBytes int64 = 4096 << 10

// BodyLimit refuses a declared Content-Length above the ceiling and wraps
// every other body so a chunked client cannot stream past it.
func BodyLimit(limit int64) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if limit <= 0 {
			ctx.Request().Next()
			return
		}
		origin := ctx.Request().Origin()
		if origin == nil {
			ctx.Request().Next()
			return
		}
		if origin.ContentLength > limit {
			_ = responses.Fail(ctx, http.StatusRequestEntityTooLarge, responses.CodeRequestTooLarge, "request body too large").Abort()
			return
		}
		if origin.Body != nil && origin.Body != http.NoBody && requestCarriesBody(origin.Method) {
			origin.Body = http.MaxBytesReader(ctx.Response().Writer(), origin.Body, limit)
		}
		ctx.Request().Next()
	}
}

func requestCarriesBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
