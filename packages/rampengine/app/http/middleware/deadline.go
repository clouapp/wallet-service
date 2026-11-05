package middleware

import (
	"context"
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"
)

// RequestDeadline bounds a request with a context deadline and runs the chain
// inline, so no handler outlives its response on a pooled gin.Context as under
// the framework's goroutine timeout. InternalError renders the deadline as 408.
func RequestDeadline(d time.Duration) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if d <= 0 {
			ctx.Request().Next()
			return
		}
		c, cancel := context.WithTimeout(ctx.Context(), d)
		defer cancel()
		ctx.WithContext(c)
		ctx.Request().Next()
	}
}
