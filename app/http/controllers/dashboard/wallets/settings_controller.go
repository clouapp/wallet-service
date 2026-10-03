package wallets

import (
	"strconv"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/repositories"
)

// SettingsController serves the dashboard wallet settings and freeze routes.
type SettingsController struct {
	wallets *repositories.WalletRepository
}

func NewSettingsController(
	wallets *repositories.WalletRepository,
) *SettingsController {
	if wallets == nil {
		panic("dashboard wallet settings controller: wallets repository is required")
	}
	return &SettingsController{
		wallets: wallets,
	}
}

// GetWalletSettings godoc
// @Summary      Get wallet settings
// @Description  Returns fee, approval, and freeze settings for a wallet
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/settings [get]
func (ctrl *SettingsController) GetWalletSettings(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	return ctx.Response().Json(http.StatusOK, http.Json{
		"fee_rate_min":       wallet.FeeRateMin,
		"fee_rate_max":       wallet.FeeRateMax,
		"fee_multiplier":     wallet.FeeMultiplier,
		"required_approvals": wallet.RequiredApprovals,
		"frozen_until":       wallet.FrozenUntil,
		"status":             wallet.Status,
	})
}

// UpdateWalletSettings godoc
// @Summary      Update wallet settings
// @Description  Updates fee rates, approval thresholds, and other wallet settings. Requires wallet or account owner/admin.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                       true  "Wallet UUID"
// @Param        request   body      UpdateWalletSettingsSwagger  true  "Settings payload"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/settings [patch]
func (ctrl *SettingsController) UpdateWalletSettings(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if errResp := controllers.Deny(ctx, policies.WalletUpdate(ctx, wallet.ID)); errResp != nil {
		return errResp
	}

	var req requests.UpdateWalletSettingsRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	if s := strings.TrimSpace(req.FeeRateMin); s != "" {
		v, _ := strconv.Atoi(s)
		if err := ctrl.wallets.SetFeeRateMin(ctx.Context(), wallet.ID, v); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeRateMin = &v
	}
	if s := strings.TrimSpace(req.FeeRateMax); s != "" {
		v, _ := strconv.Atoi(s)
		if err := ctrl.wallets.SetFeeRateMax(ctx.Context(), wallet.ID, v); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeRateMax = &v
	}
	if s := strings.TrimSpace(req.FeeMultiplier); s != "" {
		v, _ := strconv.ParseFloat(s, 64)
		if err := ctrl.wallets.SetFeeMultiplier(ctx.Context(), wallet.ID, v); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeMultiplier = &v
	}
	if s := strings.TrimSpace(req.RequiredApprovals); s != "" {
		v, _ := strconv.Atoi(s)
		if err := ctrl.wallets.SetRequiredApprovals(ctx.Context(), wallet.ID, v); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.RequiredApprovals = v
	}
	if s := strings.TrimSpace(req.FrozenUntil); s != "" {
		t, _ := time.Parse(time.RFC3339, s)
		if err := ctrl.wallets.SetFrozenUntil(ctx.Context(), wallet.ID, t); err != nil {
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FrozenUntil = &t
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"fee_rate_min":       wallet.FeeRateMin,
		"fee_rate_max":       wallet.FeeRateMax,
		"fee_multiplier":     wallet.FeeMultiplier,
		"required_approvals": wallet.RequiredApprovals,
		"frozen_until":       wallet.FrozenUntil,
		"status":             wallet.Status,
	})
}

// FreezeWallet godoc
// @Summary      Freeze a wallet
// @Description  Freezes the wallet until the specified timestamp. Requires wallet or account owner.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                true  "Wallet UUID"
// @Param        request   body      FreezeWalletSwagger   true  "Freeze payload"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/freeze [post]
func (ctrl *SettingsController) FreezeWallet(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if errResp := controllers.Deny(ctx, policies.WalletFreeze(ctx, wallet.ID)); errResp != nil {
		return errResp
	}

	var req requests.FreezeWalletRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	frozenUntil := time.Now().Add(24 * time.Hour) // default: 24h freeze
	if s := strings.TrimSpace(req.FrozenUntil); s != "" {
		t, _ := time.Parse(time.RFC3339, s)
		frozenUntil = t
	}

	if err := ctrl.wallets.SetFrozenUntil(ctx.Context(), wallet.ID, frozenUntil); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to freeze wallet"})
	}
	if err := ctrl.wallets.SetStatus(ctx.Context(), wallet.ID, "frozen"); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to freeze wallet"})
	}
	wallet.FrozenUntil = &frozenUntil
	wallet.Status = "frozen"

	return ctx.Response().Json(http.StatusOK, http.Json{
		"status":       wallet.Status,
		"frozen_until": wallet.FrozenUntil,
	})
}

// ---- Request/Response types ----

type UpdateWalletSettingsSwagger struct {
	FeeRateMin        *int       `json:"fee_rate_min,omitempty" example:"1"`
	FeeRateMax        *int       `json:"fee_rate_max,omitempty" example:"100"`
	FeeMultiplier     *float64   `json:"fee_multiplier,omitempty" example:"1.25"`
	RequiredApprovals *int       `json:"required_approvals,omitempty" example:"2"`
	FrozenUntil       *time.Time `json:"frozen_until,omitempty"`
}

type FreezeWalletSwagger struct {
	FrozenUntil *time.Time `json:"frozen_until,omitempty"`
}

type WalletSettingsResponse struct {
	FeeRateMin        *int       `json:"fee_rate_min"`
	FeeRateMax        *int       `json:"fee_rate_max"`
	FeeMultiplier     *float64   `json:"fee_multiplier"`
	RequiredApprovals int        `json:"required_approvals"`
	FrozenUntil       *time.Time `json:"frozen_until"`
	Status            string     `json:"status"`
}
