package middleware

import (
	"crypto/sha256"
	"crypto/subtle"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
)

// TenantProvisioningKeyHeader carries the platform's provisioning key.
const TenantProvisioningKeyHeader = "X-Tenant-Provisioning-Key"

// minProvisioningKeyLength is the shortest key the route accepts, configured or presented.
const minProvisioningKeyLength = 32

// ValidProvisionKey compares a presented key with the configured one in constant
// time. The key is its own secret (TENANT_PROVISIONING_KEY), never an encryption
// key; a missing or short one matches nothing.
func ValidProvisionKey(expected, presented string) bool {
	if len(expected) < minProvisioningKeyLength || len(presented) < minProvisioningKeyLength {
		return false
	}
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// TenantProvisioningKey guards the machine route of tenant provisioning: 503 while
// no usable key is configured (the route is off), 401 for a key that does not match.
func TenantProvisioningKey(configured string) http.Middleware {
	return func(ctx http.Context) {
		if len(configured) < minProvisioningKeyLength {
			_ = responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeInvalidRequest, "tenant provisioning unavailable").Abort()
			return
		}
		if !ValidProvisionKey(configured, ctx.Request().Header(TenantProvisioningKeyHeader, "")) {
			_ = responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid provisioning key").Abort()
			return
		}
		ctx.Request().Next()
	}
}
