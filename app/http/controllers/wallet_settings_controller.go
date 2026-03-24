package controllers

import (
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"
)

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
func GetWalletSettings(ctx http.Context) http.Response {
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"fee_rate_min":        wallet.FeeRateMin,
		"fee_rate_max":        wallet.FeeRateMax,
		"fee_multiplier":      wallet.FeeMultiplier,
		"required_approvals": wallet.RequiredApprovals,
		"frozen_until":        wallet.FrozenUntil,
		"status":              wallet.Status,
	})
}

// UpdateWalletSettings godoc
// @Summary      Update wallet settings
// @Description  Updates fee rates, approval thresholds, and other wallet settings. Requires wallet or account owner/admin.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                    true  "Wallet UUID"
// @Param        request   body      UpdateWalletSettingsRequest  true  "Settings payload"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/settings [patch]
func UpdateWalletSettings(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may update wallet settings"})
	}

	var req UpdateWalletSettingsRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	if req.FeeRateMin != nil {
		if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("fee_rate_min", *req.FeeRateMin); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeRateMin = req.FeeRateMin
	}
	if req.FeeRateMax != nil {
		if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("fee_rate_max", *req.FeeRateMax); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeRateMax = req.FeeRateMax
	}
	if req.FeeMultiplier != nil {
		if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("fee_multiplier", *req.FeeMultiplier); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FeeMultiplier = req.FeeMultiplier
	}
	if req.RequiredApprovals != nil {
		if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("required_approvals", *req.RequiredApprovals); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.RequiredApprovals = *req.RequiredApprovals
	}
	if req.FrozenUntil != nil {
		if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("frozen_until", req.FrozenUntil); err != nil {
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update wallet settings"})
		}
		wallet.FrozenUntil = req.FrozenUntil
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"fee_rate_min":        wallet.FeeRateMin,
		"fee_rate_max":        wallet.FeeRateMax,
		"fee_multiplier":      wallet.FeeMultiplier,
		"required_approvals": wallet.RequiredApprovals,
		"frozen_until":        wallet.FrozenUntil,
		"status":              wallet.Status,
	})
}

// FreezeWallet godoc
// @Summary      Freeze a wallet
// @Description  Freezes the wallet until the specified timestamp. Requires wallet or account owner.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string             true  "Wallet UUID"
// @Param        request   body      FreezeWalletRequest  true  "Freeze payload"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/freeze [post]
func FreezeWallet(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if walletRole != "owner" && accRole != "owner" && accRole != "admin" {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only owners and account admins may freeze wallets"})
	}

	var req FreezeWalletRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	frozenUntil := time.Now().Add(24 * time.Hour) // default: 24h freeze
	if req.FrozenUntil != nil {
		frozenUntil = *req.FrozenUntil
	}

	if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("frozen_until", frozenUntil); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to freeze wallet"})
	}
	if _, err := facades.Orm().Query().Model(wallet).Where("id = ?", wallet.ID).Update("status", "frozen"); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to freeze wallet"})
	}
	wallet.FrozenUntil = &frozenUntil
	wallet.Status = "frozen"

	return ctx.Response().Json(http.StatusOK, http.Json{
		"status":       wallet.Status,
		"frozen_until": wallet.FrozenUntil,
	})
}

// ---- Request/Response types ----

type UpdateWalletSettingsRequest struct {
	FeeRateMin        *int     `json:"fee_rate_min,omitempty" example:"1"`
	FeeRateMax        *int     `json:"fee_rate_max,omitempty" example:"100"`
	FeeMultiplier     *float64 `json:"fee_multiplier,omitempty" example:"1.25"`
	RequiredApprovals *int     `json:"required_approvals,omitempty" example:"2"`
	FrozenUntil       *time.Time `json:"frozen_until,omitempty"`
}

type FreezeWalletRequest struct {
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
