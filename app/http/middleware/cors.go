package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/spf13/cast"

	"github.com/macrowallets/waas/app/facades"
)

// Cors handles Cross-Origin Resource Sharing headers.
// When credentials mode is "include", the wildcard origin "*" is not allowed
// by browsers — we must echo back the exact request origin.
func Cors() http.Middleware {
	return func(ctx http.Context) {
		origin := ctx.Request().Header("Origin", "")

		response := ctx.Response()
		setCorsHeaders(func(key, value string) { response.Header(key, value) }, origin)

		// Respond to preflight requests immediately
		if ctx.Request().Method() == "OPTIONS" {
			ctx.Request().AbortWithStatus(http.StatusNoContent)
			return
		}

		ctx.Request().Next()
	}
}

// setCorsHeaders stamps the CORS headers through set when origin is allowed.
// TimeoutHandler uses it too, so a browser can read its 504.
func setCorsHeaders(set func(key, value string), origin string) {
	if origin == "" || !isAllowedCorsOrigin(origin) {
		return
	}
	set("Access-Control-Allow-Origin", origin)
	set("Access-Control-Allow-Credentials", "true")
	set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Account-Id, X-API-Key, X-Timestamp, X-Signature")
	set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	set("Vary", "Origin")
}

// isAllowedCorsOrigin checks if the origin is permitted.
// Reads CORS_ALLOWED_ORIGINS from env (comma-separated); defaults to localhost:3000.
func isAllowedCorsOrigin(origin string) bool {
	raw := cast.ToString(facades.Config().Env("CORS_ALLOWED_ORIGINS", ""))
	var allowed []string
	if raw == "" {
		allowed = []string{"http://localhost:3000", "http://localhost:3001"}
	} else {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				allowed = append(allowed, trimmed)
			}
		}
	}
	for _, o := range allowed {
		if o == origin {
			return true
		}
	}
	return false
}
