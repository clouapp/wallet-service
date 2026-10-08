package sweep

import (
	"math/big"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/features"
	sweep "github.com/macrowallets/waas/app/services/sweep"
	"github.com/redis/go-redis/v9"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// gasCheckRateLimitWindow caps ForceGasCheck to one on-chain read per wallet
// per minute. The limit is enforced via Redis SETNX so it is shared across all
// API nodes and Lambda handlers. When Redis is unavailable we fall through to
// the underlying handler — availability of the endpoint is more important than
// a perfect rate limit, and the chain adapter itself has short-term caching.
const gasCheckRateLimitWindow = 60 * time.Second

// SweepController serves the dashboard consolidate and gas routes.
type SweepController struct {
	sweeps sweep.Service
	redis  *redis.Client
	flags  *features.Service
}

// SweepControllerDeps is everything the dashboard sweep controller needs.
// Sweeps and Flags are required. Redis may be nil; ForceGasCheck then skips the shared rate limit.
type SweepControllerDeps struct {
	Sweeps sweep.Service
	Redis  *redis.Client
	Flags  *features.Service
}

// NewSweepController wires the dashboard consolidate and gas handlers from SweepControllerDeps.
func NewSweepController(deps SweepControllerDeps) *SweepController {
	if deps.Sweeps == nil {
		panic("dashboard sweep controller: sweep service is required")
	}
	if deps.Flags == nil {
		panic("dashboard sweep controller: feature flags are required")
	}
	return &SweepController{
		sweeps: deps.Sweeps,
		redis:  deps.Redis,
		flags:  deps.Flags,
	}
}

// ConsolidateWallet godoc
// @Summary      Consolidate wallet balances
// @Description  Sweep all eligible child addresses' balance into the wallet's base deposit address.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string                       true  "Wallet UUID"  format(uuid)
// @Param        body      body      ConsolidateRequestSwagger    true  "Consolidation request"
// @Success      200       {object}  ConsolidateResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Failure      429       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/consolidate [post]
func (ctrl *SweepController) ConsolidateWallet(ctx http.Context) http.Response {
	wallet, _ := requestctx.Wallet(ctx)
	if resp := controllers.BlockFlag(ctx, ctrl.flags, controllers.AccountIDForWallet(ctx, wallet), features.FlagSweepEnabled, features.CodeSweepPaused, "consolidate"); resp != nil {
		return resp
	}

	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.ConsolidateRequest
	defer controllers.DiscardPassphrase(&req.Passphrase)
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	callerAccountID, _ := requestctx.AccountID(ctx)

	result, err := ctrl.sweeps.ConsolidateAll(ctx.Context(), walletID, req.Asset, req.Passphrase, callerAccountID)
	if err != nil {
		if resp := controllers.MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return controllers.MapInternalError(ctx, err, "consolidate")
	}
	return ctx.Response().Success().Json(controllers.MapConsolidateResponse(result))
}

// GetGasStatus godoc
// @Summary      Get wallet gas readiness
// @Description  Returns the gas readiness status, native balance, and configured threshold for a wallet's base address.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string  true  "Wallet UUID"  format(uuid)
// @Success      200       {object}  GasStatusResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/gas-status [get]
func (ctrl *SweepController) GetGasStatus(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	status, err := ctrl.sweeps.RefreshGasStatus(ctx.Context(), walletID)
	if err != nil {
		if resp := controllers.MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return controllers.MapInternalError(ctx, err, "gas_status")
	}
	return ctx.Response().Success().Json(controllers.GasStatusBody(status))
}

// ForceGasCheck godoc
// @Summary      Force refresh of wallet gas status
// @Description  Action variant of gas-status. Forces an on-chain read. Rate-limited per wallet.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string  true  "Wallet UUID"  format(uuid)
// @Success      200       {object}  GasStatusResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Failure      429       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/gas-check [post]
func (ctrl *SweepController) ForceGasCheck(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	if rdb := ctrl.redis; rdb != nil {
		key := "vault:ratelimit:gas-check:" + walletID.String()
		ok, setErr := rdb.SetNX(ctx.Context(), key, "1", gasCheckRateLimitWindow).Result()
		if setErr == nil && !ok {
			return responses.Send(ctx, http.StatusTooManyRequests, http.Json{
				"error":               "rate_limited",
				"limit_type":          "gas_check",
				"retry_after_seconds": int(gasCheckRateLimitWindow / time.Second),
			})
		}
	}

	return ctrl.GetGasStatus(ctx)
}

// PreviewWithdraw godoc
// @Summary      Preview a withdrawal
// @Description  Returns the planned sweep strategy without executing any transaction.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string                            true  "Wallet UUID"  format(uuid)
// @Param        body      body      WithdrawPreviewRequestSwagger     true  "Preview request"
// @Success      200       {object}  WithdrawPreviewResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/withdraw/preview [post]
func (ctrl *SweepController) PreviewWithdraw(ctx http.Context) http.Response {
	walletID, err := requests.RouteUUID(ctx, "walletId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.WithdrawPreviewRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid amount"})
	}

	callerAccountID, _ := requestctx.AccountID(ctx)

	const previewHasNoDestination = ""
	plan, err := ctrl.sweeps.PlanForWithdrawal(ctx.Context(), walletID, req.Asset, amount, previewHasNoDestination, callerAccountID)
	if err != nil {
		if resp := controllers.MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return controllers.MapInternalError(ctx, err, "preview_withdraw")
	}
	return ctx.Response().Success().Json(controllers.WithdrawPreviewBody(plan))
}
