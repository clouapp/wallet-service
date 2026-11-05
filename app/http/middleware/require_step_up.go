package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
)

// CodeStepUpRequired is the 403 a settings write answers when the session has
// no open step-up window.
const CodeStepUpRequired = "step_up_required"

// stepUpFlagReader is the settings-2fa-required flag at the moment of use.
type stepUpFlagReader interface {
	Settings2FARequired(ctx context.Context, accountID uuid.UUID) (bool, error)
}

// stepUpWindows is the step-up window of a session.
type stepUpWindows interface {
	Status(sid uuid.UUID) (*time.Time, error)
}

// RequireStepUp refuses a sensitive write unless the session proved a second
// factor in the last 15 minutes. With the settings-2fa-required flag off the
// request goes through. With it on, a user without confirmed TOTP gets 403
// two_factor_enrollment_required (as TOTPEnrollment does) and a session with no
// open window 403 step_up_required, never 401. A route with no account (the
// /v1/platform group) reads only the global flag row. A failed flag or window
// read is 500. Mount it after SessionAuth (and the account context, if any).
func RequireStepUp(flags stepUpFlagReader, windows stepUpWindows) http.Middleware {
	if flags == nil {
		panic("require step up: feature flags are required")
	}
	if windows == nil {
		panic("require step up: step-up service is required")
	}
	return func(ctx http.Context) {
		accountID, _ := requestctx.AccountID(ctx)
		required, err := flags.Settings2FARequired(ctx.Context(), accountID)
		if err != nil {
			slog.Error("step-up flag check failed", "account_id", accountID, "error", err)
			_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal").Abort()
			return
		}
		if !required {
			ctx.Request().Next()
			return
		}
		user, ok := requestctx.User(ctx)
		if !ok || user == nil || user.ID == uuid.Nil {
			_ = responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated").Abort()
			return
		}
		if !user.TotpEnabled {
			_ = responses.Fail(ctx, http.StatusForbidden, "two_factor_enrollment_required", CodeTwoFactorEnrollmentRequired).Abort()
			return
		}
		sid, _ := requestctx.SessionID(ctx)
		expiresAt, err := windows.Status(sid)
		if err != nil {
			slog.Error("step-up window check failed", "user_id", user.ID, "error", err)
			_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal").Abort()
			return
		}
		if expiresAt == nil {
			_ = responses.Fail(ctx, http.StatusForbidden, CodeStepUpRequired, CodeStepUpRequired).Abort()
			return
		}
		ctx.Request().Next()
	}
}
