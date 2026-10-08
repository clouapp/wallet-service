package middleware

import (
	"context"
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

// RequestTimeout installs a deadline on the request context and continues the
// chain on the same goroutine. It never answers: handlers and RPC calls that
// honour ctx stop on their own, and the hard cut is TimeoutHandler at the
// net/http server (runLocal). In Lambda mode this deadline is all there is;
// Lambda enforces its own invocation timeout. A non-positive duration installs
// no deadline. The chain continues without building the Goravel request,
// because that build JSON-decodes the body.
//
// Running the chain on a second goroutine and returning at the deadline was
// the previous design: the goroutine kept writing to a gin context the pool
// had already handed to the next request.
func RequestTimeout(timeout time.Duration) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if timeout <= 0 {
			continueChain(ctx)
			return
		}

		timeoutCtx, cancel := context.WithTimeout(ctx.Context(), timeout)
		defer cancel()
		ctx.WithContext(timeoutCtx)
		continueChain(ctx)
	}
}
