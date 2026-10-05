package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// RequestTimeout bounds the rest of the global chain. A non-positive duration
// does not install a deadline.
func RequestTimeout(timeout time.Duration) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if timeout <= 0 {
			ctx.Request().Next()
			return
		}

		timeoutCtx, cancel := context.WithTimeout(ctx.Context(), timeout)
		defer cancel()
		ctx.WithContext(timeoutCtx)

		done := make(chan struct{})
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					RecoverPanic(ctx, recovered)
				}
				close(done)
			}()
			ctx.Request().Next()
		}()

		select {
		case <-done:
		case <-timeoutCtx.Done():
			if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				_ = responses.Send(ctx, http.StatusGatewayTimeout, contractshttp.Json{
					"error": "request timed out",
				}).Abort()
			}
		}
	}
}
