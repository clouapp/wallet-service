package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macromarkets/vault/pkg/security"
)

// AWS API Gateway host pattern (should be allowed in Host header)
var awsAPIGatewayPattern = regexp.MustCompile(`(?i)execute-api\.[a-z0-9-]+\.amazonaws\.com`)

// AntiSQLInjection blocks requests with SQL injection patterns in headers, query params, or body
func AntiSQLInjection() http.Middleware {
	return func(ctx http.Context) {
		// Check headers
		headers := ctx.Request().Headers()
		for key, vals := range headers {
			for _, val := range vals {
				// Skip AWS API Gateway host headers
				if strings.EqualFold(key, "host") && awsAPIGatewayPattern.MatchString(val) {
					continue
				}
				if security.IsSQLInjection(val) {
					ctx.Request().AbortWithStatus(http.StatusForbidden)
					ctx.Response().Json(http.StatusForbidden, http.Json{
						"error":   "Possible SQL injection detected",
						"message": fmt.Sprintf("Suspicious pattern detected in header: %s", key),
					})
					return
				}
			}
		}

		// Check query parameters
		queries := ctx.Request().Queries()
		for key, val := range queries {
			if security.IsSQLInjection(val) {
				ctx.Request().AbortWithStatus(http.StatusForbidden)
				ctx.Response().Json(http.StatusForbidden, http.Json{
					"error":   "Possible SQL injection detected",
					"message": fmt.Sprintf("Suspicious pattern detected in query param: %s", key),
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
						if security.ContainsSQLInjection(payload) {
							ctx.Request().AbortWithStatus(http.StatusForbidden)
							ctx.Response().Json(http.StatusForbidden, http.Json{
								"error":   "Possible SQL injection detected",
								"message": "Suspicious patterns detected in request body",
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
