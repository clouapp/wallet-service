package middleware

import (
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

// Global chain order from the alignment plan: request timeout, then the
// inbound provider signature, then request id, security headers, body limit,
// and CORS. The signature step sits before any middleware that builds the
// Goravel request, so an ingest body is not parsed first. Recover is
// installed separately.
const (
	chainRequestTimeout    = "request_timeout"
	chainProviderSignature = "provider_signature"
	chainRequestID         = "request_id"
	chainSecurityHeaders   = "security_headers"
	chainBodyLimit         = "body_limit"
	chainCORS              = "cors"
)

type chainLink struct {
	name       string
	middleware contractshttp.Middleware
}

func globalChainLinks(timeout time.Duration, signature InboundSignatureDeps, corsOrigins []string) []chainLink {
	return []chainLink{
		{name: chainRequestTimeout, middleware: RequestTimeout(timeout)},
		{name: chainProviderSignature, middleware: ProviderSignature(signature)},
		{name: chainRequestID, middleware: RequestID()},
		{name: chainSecurityHeaders, middleware: SecurityHeaders()},
		{name: chainBodyLimit, middleware: BodyLimit(MaxGlobalBodyBytes)},
		{name: chainCORS, middleware: Cors(corsOrigins)},
	}
}

// GlobalChain is the middleware installed once through WithMiddleware Use.
// timeout is the configured http.request_timeout. Zero disables the deadline,
// matching the gin driver. signature carries the inbound webhook lookups and
// corsOrigins the origins Cors echoes back.
func GlobalChain(timeout time.Duration, signature InboundSignatureDeps, corsOrigins []string) []contractshttp.Middleware {
	links := globalChainLinks(timeout, signature, corsOrigins)
	chain := make([]contractshttp.Middleware, len(links))
	for i, link := range links {
		chain[i] = link.middleware
	}
	return chain
}
