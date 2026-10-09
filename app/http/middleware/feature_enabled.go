package middleware

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/features"
)

// walletFlagGate is the flag gate of a wallet's money movement
// (features.Service.GateWallet).
type walletFlagGate interface {
	GateWallet(ctx context.Context, wallet *models.Wallet, callerAccountID uuid.UUID, key, code string) error
}

// FeatureEnabled pauses a money-moving wallet route while its flag is off.
// It is the last guard of the route, so it runs after authentication, the
// wallet scope and the permission check, and before the body is read. The
// flag is the wallet's account flag (the caller's account for a wallet
// without one), and an explicit global false pauses it too. A paused flag is
// 409 with code as both code and message. A flag read that fails is the
// generic 500, logged under endpoint.
func FeatureEnabled(flags walletFlagGate, key, code, endpoint string) http.Middleware {
	if flags == nil {
		panic("feature enabled: feature flags are required")
	}
	return func(ctx http.Context) {
		wallet, _ := requestctx.Wallet(ctx)
		callerAccountID, _ := requestctx.AccountID(ctx)
		err := flags.GateWallet(ctx.Context(), wallet, callerAccountID, key, code)
		if err == nil {
			ctx.Request().Next()
			return
		}
		var gate *features.GateError
		if errors.As(err, &gate) && gate != nil && gate.Code != "" {
			_ = responses.Fail(ctx, http.StatusConflict, gate.Code, gate.Code).Abort()
			return
		}
		slog.Error("controller internal error", "endpoint", endpoint, "error", err)
		_ = responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternalError, "internal_error").Abort()
	}
}
