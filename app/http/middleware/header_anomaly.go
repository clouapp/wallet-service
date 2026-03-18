package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
)

// HeaderAnomaly detects and blocks requests with header anomalies indicating
// automation tools (bots, Playwright, Puppeteer, etc.)
//
// According to MDN (https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Sec-Fetch-User):
// - Sec-Fetch-User is ONLY sent for navigation requests (Sec-Fetch-Mode: navigate)
// - When a request is triggered by fetch/XHR, browsers MUST omit the header per spec
//
// Real browsers NEVER send these headers on fetch/XHR requests:
// - sec-fetch-user: ?1 (navigation-only, never on fetch/XHR)
// - upgrade-insecure-requests: 1 (navigation-only, never on fetch/XHR)
//
// These headers appearing on requests with Sec-Fetch-Mode: cors indicate
// automated tools replaying browser headers incorrectly.
func HeaderAnomaly(enabled bool) http.Middleware {
	return func(ctx http.Context) {
		if !enabled {
			ctx.Request().Next()
			return
		}

		if HasHeaderAnomalies(ctx) {
			ctx.Request().AbortWithStatus(http.StatusForbidden)
			ctx.Response().Json(http.StatusForbidden, http.Json{
				"error": "Invalid request headers detected",
			})
			return
		}

		ctx.Request().Next()
	}
}

// HasHeaderAnomalies checks if a request has header anomalies indicating bot activity
func HasHeaderAnomalies(ctx http.Context) bool {
	// Check if sec-fetch-mode is cors (XHR/fetch request)
	secFetchMode := strings.ToLower(ctx.Request().Header("Sec-Fetch-Mode", ""))

	if secFetchMode == "cors" {
		// Block sec-fetch-user (should never exist on fetch/XHR)
		// This header is ONLY sent for navigation requests
		if ctx.Request().Header("Sec-Fetch-User", "") != "" {
			return true
		}

		// Block upgrade-insecure-requests (navigation-only header)
		// This header is ONLY sent for navigation requests
		if ctx.Request().Header("Upgrade-Insecure-Requests", "") != "" {
			return true
		}
	}

	// Additional bot detection: Check for suspicious header combinations
	// Real browsers don't typically send these together with WebSocket upgrades
	if ctx.Request().Header("Upgrade", "") == "websocket" {
		// WebSocket requests from real browsers have specific patterns
		// Bots often add extra headers that browsers don't send

		// Check for sec-fetch-dest mismatch
		// Real WebSocket upgrades should have sec-fetch-dest: websocket (if header is present)
		secFetchDest := strings.ToLower(ctx.Request().Header("Sec-Fetch-Dest", ""))
		if secFetchDest != "" && secFetchDest != "websocket" && secFetchDest != "empty" {
			return true
		}

		// Check for sec-fetch-mode mismatch for WebSocket
		// Real WebSocket upgrades should have sec-fetch-mode: websocket (if header is present)
		if secFetchMode != "" && secFetchMode != "websocket" && secFetchMode != "cors" {
			return true
		}
	}

	return false
}

// HasWebSocketHeaderAnomalies is a convenience function specifically for WebSocket requests
// Empty origin is suspicious for WebSocket (browsers always send it)
func HasWebSocketHeaderAnomalies(ctx http.Context) bool {
	if ctx.Request().Header("Origin", "") == "" {
		return true
	}

	return HasHeaderAnomalies(ctx)
}
