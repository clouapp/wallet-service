package middleware

import (
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/http"
)

func CacheControl(maxAge time.Duration) http.Middleware {
	return func(ctx http.Context) {
		if maxAge <= 0 {
			ctx.Response().Header("Cache-Control", "no-store, no-cache, must-revalidate")
			ctx.Response().Header("Pragma", "no-cache")
		} else {
			ctx.Response().Header("Cache-Control",
				fmt.Sprintf("private, max-age=%d", int(maxAge.Seconds())))
		}
		ctx.Request().Next()
	}
}
