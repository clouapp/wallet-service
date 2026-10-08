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
		origin := ctx.Request().Origin()
		response := ctx.Response()
		setSecurityHeaders(func(key, value string) { response.Header(key, value) }, origin != nil && origin.TLS != nil)
		ctx.Request().Next()
	}
}

// setSecurityHeaders stamps the defensive headers through set. TimeoutHandler
// uses it too, so its 504 carries the same headers as any other answer.
func setSecurityHeaders(set func(key, value string), overTLS bool) {
	set("Content-Security-Policy", headerCSP)
	set("X-Frame-Options", headerFrameOptions)
	set("X-Content-Type-Options", headerContentTypeOptions)
	set("Referrer-Policy", headerReferrerPolicy)
	set("Cache-Control", headerCacheControl)
	if overTLS {
		set("Strict-Transport-Security", headerHSTS)
	}
}
