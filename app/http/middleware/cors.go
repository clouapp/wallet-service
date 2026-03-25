package middleware

import (
	"os"
	"strings"

	"github.com/goravel/framework/contracts/http"
)

// Cors handles Cross-Origin Resource Sharing headers.
// When credentials mode is "include", the wildcard origin "*" is not allowed
// by browsers — we must echo back the exact request origin.
func Cors() http.Middleware {
	return func(ctx http.Context) {
		origin := ctx.Request().Header("Origin", "")

		if origin != "" && isAllowedCorsOrigin(origin) {
			ctx.Response().Header("Access-Control-Allow-Origin", origin)
			ctx.Response().Header("Access-Control-Allow-Credentials", "true")
			ctx.Response().Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Account-Id, X-API-Key, X-Timestamp, X-Signature")
			ctx.Response().Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			ctx.Response().Header("Vary", "Origin")
		}

		// Respond to preflight requests immediately
		if ctx.Request().Method() == "OPTIONS" {
			ctx.Request().AbortWithStatus(http.StatusNoContent)
			return
		}

		ctx.Request().Next()
	}
}

// isAllowedCorsOrigin checks if the origin is permitted.
// Reads CORS_ALLOWED_ORIGINS from env (comma-separated); defaults to localhost:3000.
func isAllowedCorsOrigin(origin string) bool {
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
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
