package middleware

import (
	"strings"

	"github.com/goravel/framework/contracts/http"
)

// Domain validates that incoming requests are from allowed domains/origins
// Helps prevent unauthorized cross-origin requests and domain spoofing
func Domain(allowedDomains []string) http.Middleware {
	return func(ctx http.Context) {
		origin := ctx.Request().Header("Origin", "")

		// Block requests with empty origin (except for same-origin requests)
		if origin == "" {
			if !isSameOrigin(ctx) {
				ctx.Request().AbortWithStatus(http.StatusForbidden)
				ctx.Response().Json(http.StatusForbidden, http.Json{
					"error": "Empty origin not allowed",
				})
				return
			}
			ctx.Request().Next()
			return
		}

		// Extract domain from origin
		domain := extractDomain(origin)

		// Check if the domain matches any of the allowed domains
		if !isAllowedDomain(domain, allowedDomains) {
			ctx.Request().AbortWithStatus(http.StatusForbidden)
			ctx.Response().Json(http.StatusForbidden, http.Json{
				"error": "Origin not allowed",
			})
			return
		}

		ctx.Request().Next()
	}
}

// isSameOrigin checks if the request is from the same origin
func isSameOrigin(ctx http.Context) bool {
	referer := ctx.Request().Header("Referer", "")
	if referer == "" {
		return false
	}

	// Extract host from referer
	refererHost := strings.TrimPrefix(referer, "http://")
	refererHost = strings.TrimPrefix(refererHost, "https://")
	refererHost = strings.Split(refererHost, "/")[0]

	// Compare with request host
	return refererHost == ctx.Request().Host()
}

// extractDomain extracts the domain from the origin URL
func extractDomain(origin string) string {
	domain := strings.TrimPrefix(origin, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.Split(domain, ":")[0]
	return domain
}

// isAllowedDomain checks if the domain is in the allowed domains list
// Supports wildcard matching (e.g., "*.example.com")
func isAllowedDomain(domain string, allowedDomains []string) bool {
	for _, allowedDomain := range allowedDomains {
		// Exact match
		if domain == allowedDomain {
			return true
		}

		// Wildcard match (*.example.com)
		if strings.HasPrefix(allowedDomain, "*.") {
			baseDomain := strings.TrimPrefix(allowedDomain, "*.")
			if strings.HasSuffix(domain, baseDomain) {
				return true
			}
		}

		// Suffix match (example.com matches subdomain.example.com)
		if strings.HasSuffix(domain, allowedDomain) {
			return true
		}
	}
	return false
}
