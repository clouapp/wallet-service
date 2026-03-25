package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/pkg/security"
)

// AntiXSS intercepts requests with potential XSS attacks in headers, query params, or body
// and blocks them from being processed
func AntiXSS() http.Middleware {
	return func(ctx http.Context) {
		// Check headers
		headers := ctx.Request().Headers()
		for key, vals := range headers {
			for _, val := range vals {
				if security.IsDangerousHTML(val) {
					ctx.Request().AbortWithStatus(http.StatusForbidden)
					ctx.Response().Json(http.StatusForbidden, http.Json{
						"error":   "Possible XSS attack detected",
						"message": fmt.Sprintf("Dangerous content detected in header: %s", key),
					})
					return
				}
			}
		}

		// Check query parameters
		queries := ctx.Request().Queries()
		for key, val := range queries {
			if security.IsDangerousHTML(val) {
				ctx.Request().AbortWithStatus(http.StatusForbidden)
				ctx.Response().Json(http.StatusForbidden, http.Json{
					"error":   "Possible XSS attack detected",
					"message": fmt.Sprintf("Dangerous content detected in query param: %s", key),
				})
				return
			}
		}

		// Check request body (JSON)
		method := ctx.Request().Method()
		if method == "POST" || method == "PUT" || method == "PATCH" {
			contentType := ctx.Request().Header("Content-Type", "")
			if strings.Contains(contentType, "application/json") {
				// Read body
				bodyBytes, err := io.ReadAll(ctx.Request().Origin().Body)
				if err == nil && len(bodyBytes) > 0 {
					var payload map[string]interface{}
					if err := json.Unmarshal(bodyBytes, &payload); err == nil {
						if security.ContainsDangerousHTML(payload) {
							ctx.Request().AbortWithStatus(http.StatusForbidden)
							ctx.Response().Json(http.StatusForbidden, http.Json{
								"error":   "Possible XSS attack detected",
								"message": "Dangerous content detected in request body",
							})
							return
						}
					}
				}
			}
		}

		ctx.Request().Next()
	}
}
