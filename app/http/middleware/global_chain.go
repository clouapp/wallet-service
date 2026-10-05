package middleware

import (
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

// Global chain order from the alignment plan: request timeout, request id,
// security headers, body limit, CORS. Recover is installed separately.
const (
	chainRequestTimeout  = "request_timeout"
	chainRequestID       = "request_id"
	chainSecurityHeaders = "security_headers"
	chainBodyLimit       = "body_limit"
	chainCORS            = "cors"
)

type chainLink struct {
	name       string
	middleware contractshttp.Middleware
}

func globalChainLinks(timeout time.Duration) []chainLink {
	return []chainLink{
		{name: chainRequestTimeout, middleware: RequestTimeout(timeout)},
		{name: chainRequestID, middleware: RequestID()},
		{name: chainSecurityHeaders, middleware: SecurityHeaders()},
		{name: chainBodyLimit, middleware: BodyLimit(MaxGlobalBodyBytes)},
		{name: chainCORS, middleware: Cors()},
	}
}

// GlobalChain is the middleware installed once through WithMiddleware Use.
// timeout is the configured http.request_timeout. Zero disables the deadline,
// matching the gin driver.
func GlobalChain(timeout time.Duration) []contractshttp.Middleware {
	links := globalChainLinks(timeout)
	chain := make([]contractshttp.Middleware, len(links))
	for i, link := range links {
		chain[i] = link.middleware
	}
	return chain
}
