package middleware

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
)

// CodeTwoFactorEnrollmentRequired is the 403 S3.4.5 names when a member has
// no confirmed TOTP and either control is on.
const CodeTwoFactorEnrollmentRequired = "two_factor_enrollment_required"

const totpEnrollmentPath = "/v1/users/me/totp"

// totpFlagReader is the user-2fa-required flag at the moment of use.
type totpFlagReader interface {
	User2FARequired(ctx context.Context, accountID uuid.UUID) (bool, error)
}

// totpPolicyReader is account_security.require_2fa at the moment of use.
type totpPolicyReader interface {
	Require2FA(ctx context.Context, accountID uuid.UUID) (bool, error)
}

// TOTPEnrollment blocks account routes for a member who has not confirmed
// TOTP when the user-2fa-required flag or account_security.require_2fa is
// on. Confirmed TOTP is users.totp_enabled. /v1/users/me/totp and its
// children stay open so the member can enroll. A missing flag row and a
// missing setting use their defaults (both false). A read failure is 500.
func TOTPEnrollment(flags totpFlagReader, policy totpPolicyReader) http.Middleware {
	if flags == nil {
		panic("totp enrollment: feature flags are required")
	}
	if policy == nil {
		panic("totp enrollment: account settings are required")
	}
	return func(ctx http.Context) {
		if isTOTPEnrollmentPath(ctx.Request().Path()) {
			ctx.Request().Next()
			return
		}
		user, ok := requestctx.User(ctx)
		if !ok || user == nil || user.ID == uuid.Nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
			return
		}
		if user.TotpEnabled {
			ctx.Request().Next()
			return
		}
		account, ok := requestctx.Account(ctx)
		if !ok || account == nil || account.ID == uuid.Nil {
			_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal").Abort()
			return
		}
		required, err := enrollmentRequired(ctx.Context(), flags, policy, account.ID)
		if err != nil {
			slog.Error("totp enrollment check failed", "account_id", account.ID, "error", err)
			_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal").Abort()
			return
		}
		if required {
			_ = responses.Fail(ctx, http.StatusForbidden, "two_factor_enrollment_required", CodeTwoFactorEnrollmentRequired).Abort()
			return
		}
		ctx.Request().Next()
	}
}

func enrollmentRequired(ctx context.Context, flags totpFlagReader, policy totpPolicyReader, accountID uuid.UUID) (bool, error) {
	flagOn, err := flags.User2FARequired(ctx, accountID)
	if err != nil {
		return false, err
	}
	policyOn, err := policy.Require2FA(ctx, accountID)
	if err != nil {
		return false, err
	}
	return flagOn || policyOn, nil
}

func isTOTPEnrollmentPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	path = strings.TrimSuffix(path, "/")
	return path == totpEnrollmentPath || strings.HasPrefix(path, totpEnrollmentPath+"/")
}
