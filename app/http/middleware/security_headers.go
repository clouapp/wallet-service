package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"
)

const (
	headerCSP                = "default-src 'none'; frame-ancestors 'none'"
	headerFrameOptions       = "DENY"
	headerContentTypeOptions = "nosniff"
	headerReferrerPolicy     = "no-referrer"
	headerCacheControl       = "no-store"
	headerHSTS               = "max-age=31536000; includeSubDomains"
)

// SecurityHeaders stamps the defensive headers before the rest of the chain,
// so a later rejection still carries them. HSTS is sent only on a connection
// that is already TLS.
func SecurityHeaders() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		response := ctx.Response()
		response.Header("Content-Security-Policy", headerCSP)
		response.Header("X-Frame-Options", headerFrameOptions)
		response.Header("X-Content-Type-Options", headerContentTypeOptions)
		response.Header("Referrer-Policy", headerReferrerPolicy)
		response.Header("Cache-Control", headerCacheControl)
		if origin := ctx.Request().Origin(); origin != nil && origin.TLS != nil {
			response.Header("Strict-Transport-Security", headerHSTS)
		}
		ctx.Request().Next()
	}
}
