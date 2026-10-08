package middleware

import (
	"slices"

	"github.com/goravel/framework/contracts/http"
)

// Cors handles Cross-Origin Resource Sharing headers for the origins in
// allowed (http.cors_allowed_origins, read once at boot).
//
// This is not the gin driver's Cors() on purpose. Its rs/cors engine answers
// differently from what the front end gets today: it echoes the requested
// method and headers instead of the fixed allow-lists below, adds
// Access-Control-Allow-Private-Network, stamps Vary on a disallowed origin,
// sends no allow-lists on a simple request, and lets an OPTIONS without
// Access-Control-Request-Method fall through to the router. TimeoutHandler also
// needs the same headers on a 504, outside gin. Switching would change the
// wire, so cors_test.go pins today's headers and the driver's "cors" config key
// stays unset (the driver then passes every request through).
//
// When credentials mode is "include", the wildcard origin "*" is not allowed
// by browsers, so the exact request origin is echoed.
func Cors(allowed []string) http.Middleware {
	return func(ctx http.Context) {
		origin := ctx.Request().Header("Origin", "")

		response := ctx.Response()
		setCorsHeaders(func(key, value string) { response.Header(key, value) }, origin, allowed)

		// Respond to preflight requests immediately
		if ctx.Request().Method() == "OPTIONS" {
			ctx.Request().AbortWithStatus(http.StatusNoContent)
			return
		}

		ctx.Request().Next()
	}
}

// setCorsHeaders stamps the CORS headers through set when origin is in allowed.
// TimeoutHandler uses it too, so a browser can read its 504.
func setCorsHeaders(set func(key, value string), origin string, allowed []string) {
	if origin == "" || !slices.Contains(allowed, origin) {
		return
	}
	set("Access-Control-Allow-Origin", origin)
	set("Access-Control-Allow-Credentials", "true")
	set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Account-Id, X-API-Key, X-Timestamp, X-Signature")
	set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	set("Vary", "Origin")
}
