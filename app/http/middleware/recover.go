package middleware

import (
	"net/http"

	contractshttp "github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
)

// RecoverPanic is the WithMiddleware recovery callback. It answers 500 and
// logs the method, path, request id, and panic value. It does not log the
// request, so headers and bodies stay out of the line.
func RecoverPanic(ctx contractshttp.Context, recovered any) {
	method, path, requestID := "", "", ""
	if ctx != nil && ctx.Request() != nil {
		if origin := ctx.Request().Origin(); origin != nil {
			method = origin.Method
			if origin.URL != nil {
				path = origin.URL.Path
			}
			requestID = RequestIDFromContext(origin.Context())
		}
	}
	reportPanic(ctx, method, path, requestID, recovered)
	if ctx == nil || ctx.Request() == nil {
		return
	}
	_ = responses.Send(ctx, http.StatusInternalServerError, contractshttp.Json{
		"error": "internal error",
	}).Abort()
}

func reportPanic(ctx contractshttp.Context, method, path, requestID string, recovered any) {
	defer func() { _ = recover() }()
	logger := appfacades.Log()
	if logger == nil {
		return
	}
	logger.WithContext(ctx).Errorf(
		"http handler panic: method=%s path=%s request_id=%s panic=%v",
		method, path, requestID, recovered,
	)
}
