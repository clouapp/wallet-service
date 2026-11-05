package middleware

import (
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
)

// HeaderAnomaly blocks requests whose headers indicate automation tools
// (bots, Playwright, Puppeteer, etc.) replaying browser headers incorrectly.
//
// Per MDN, Sec-Fetch-User and Upgrade-Insecure-Requests are navigation-only
// and MUST be omitted by browsers on fetch/XHR. Their presence with
// Sec-Fetch-Mode: cors is a strong automation signal.
func HeaderAnomaly(enabled bool) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		if !enabled {
			ctx.Request().Next()
			return
		}

		if HasHeaderAnomalies(ctx) {
			responses.Error(ctx, contractshttp.StatusForbidden, "invalid request headers detected").Abort()
			return
		}

		ctx.Request().Next()
	}
}

// HasHeaderAnomalies reports whether the request carries header combinations
// that real browsers never produce.
func HasHeaderAnomalies(ctx contractshttp.Context) bool {
	return hasHeaderAnomalies(func(name string) string {
		return ctx.Request().Header(name, "")
	})
}

// hasHeaderAnomalies is the pure detection logic, decoupled from the HTTP
// context so it can be exercised in unit tests with a simple header lookup
// function.
func hasHeaderAnomalies(get func(name string) string) bool {
	secFetchMode := strings.ToLower(get("Sec-Fetch-Mode"))

	if secFetchMode == "cors" {
		if get("Sec-Fetch-User") != "" {
			return true
		}
		if get("Upgrade-Insecure-Requests") != "" {
			return true
		}
	}

	if get("Upgrade") == "websocket" {
		secFetchDest := strings.ToLower(get("Sec-Fetch-Dest"))
		if secFetchDest != "" && secFetchDest != "websocket" && secFetchDest != "empty" {
			return true
		}
		if secFetchMode != "" && secFetchMode != "websocket" && secFetchMode != "cors" {
			return true
		}
	}

	return false
}
