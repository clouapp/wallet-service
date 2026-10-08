package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// RequireEnabledTOTP is the dashboard re-authentication step for changing a
// whitelist or a webhook endpoint. It is registered on those routes, after
// the permission middleware. A caller who already has TOTP on must send
// totp_code; a missing or wrong code is refused with the same 401 login and
// withdrawal already return, and the code is checked by the verifier those
// share. A caller without TOTP continues. A missing whitelist entry or
// webhook is left to the handler, which answers 404. External token routes
// do not register this middleware.
func RequireEnabledTOTP() http.Middleware {
	users := container.MustMake[*usersvc.Service]()
	verifier := container.MustMake[*authsvc.SecondFactorVerifier]()
	entries := container.MustMake[*walletrecords.Whitelist]()
	hooks := container.MustMake[*walletrecords.Webhooks]()
	return func(ctx http.Context) {
		if changeTargetMissing(ctx, entries, hooks) {
			ctx.Request().Next()
			return
		}
		userID := SessionUserID(ctx)
		if userID == uuid.Nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "user not found"})
			return
		}
		user, err := users.FindByID(ctx.Context(), userID)
		if err != nil {
			slog.Error("dashboard totp: load user", "error", err)
			abortWithJSON(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
			return
		}
		if user == nil {
			abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "user not found"})
			return
		}
		if !user.TotpEnabled {
			ctx.Request().Next()
			return
		}
		if err := verifier.Verify(user, dashboardTOTPCode(ctx), ""); err != nil {
			abortDashboardTOTP(ctx, err)
			return
		}
		ctx.Request().Next()
	}
}

// changeTargetMissing reports a delete whose entry or webhook is not there,
// or whose id is not a UUID. The handler answers 404 or 400. Create has no
// existing row, so it is never missing.
func changeTargetMissing(ctx http.Context, entries *walletrecords.Whitelist, hooks *walletrecords.Webhooks) bool {
	wallet, ok := requestctx.Wallet(ctx)
	if !ok || wallet == nil {
		return false
	}
	if strings.TrimSpace(ctx.Request().Route("entryId")) != "" {
		entryID, err := requests.RouteUUID(ctx, "entryId")
		if err != nil {
			return true
		}
		entry, err := entries.FindByIDAndWallet(ctx.Context(), entryID, wallet.ID)
		return err != nil || entry == nil
	}
	if strings.TrimSpace(ctx.Request().Route("webhookId")) != "" {
		webhookID, err := requests.RouteUUID(ctx, "webhookId")
		if err != nil {
			return true
		}
		cfg, err := hooks.FindByIDAndWallet(ctx.Context(), webhookID, wallet.ID)
		return err != nil || cfg == nil
	}
	return false
}

// dashboardTOTPCode reads totp_code from the JSON body and puts the body
// back for the handler. The code is not logged. A query string is ignored.
func dashboardTOTPCode(ctx http.Context) string {
	if ctx == nil || ctx.Request() == nil {
		return ""
	}
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return ""
	}
	body, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var payload struct {
		Code string `json:"totp_code"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Code)
}

func abortDashboardTOTP(ctx http.Context, err error) {
	switch {
	case errors.Is(err, authsvc.ErrInvalidSecondFactor), errors.Is(err, authsvc.ErrSecondFactorNotEnrolled):
		abortWithJSON(ctx, http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	case errors.Is(err, authsvc.ErrSecondFactorLocked):
		abortWithJSON(ctx, http.StatusTooManyRequests, http.Json{"error": "too many 2FA attempts, sign in again later"})
	default:
		slog.Error("dashboard totp", "error", err)
		abortWithJSON(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
}
