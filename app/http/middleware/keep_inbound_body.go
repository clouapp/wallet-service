package middleware

import (
	"net/http"
	"strings"
)

// KeepInboundBody hands an inbound provider webhook to the router with its body
// untouched. The gin driver builds its request before any middleware of this
// application runs (its own recover, timeout, CORS and TLS steps call
// ctx.Request()), and building it reads an application/json body, closes it, and
// puts it back only when it decodes as a JSON object: a JSON array, which is every
// Helius delivery, was left closed, and ProviderSignature refused it as an invalid
// body. Marked as octet-stream before the router sees it, the body is left to
// ProviderSignature, which reads it whole and checks the signature over those
// bytes; no provider signs the content type.
func KeepInboundBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, inboundWebhookPrefix) {
			r.Header.Set("Content-Type", "application/octet-stream")
		}
		next.ServeHTTP(w, r)
	})
}
