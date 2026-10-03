package controllers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletsettings"
	"github.com/macrowallets/waas/pkg/numeric"
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
// @Router       /v1/wallets/{walletId}/settings [get]
func GetWalletSettings(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	return ctx.Response().Json(http.StatusOK, walletSettingsResponse(wallet))
}

// UpdateWalletSettings godoc
// @Summary      Update wallet settings
// @Description  Updates the wallet's name and fee settings. Requires wallet or account owner/admin. Only the listed fields are accepted; each may be omitted (unchanged) or null (reset to the network default). fee_multiplier (1.0000–5.0000, up to 4 decimals) scales the gas price on EVM chains and the fee rate on Bitcoin, in fee estimates and in the withdrawals themselves; it does not apply to Solana. fee_rate_min/fee_rate_max (1–10000 sat/vB, min ≤ max) clamp the Bitcoin fee rate. Freezing uses POST /freeze.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                       true  "Wallet UUID"
// @Param        request   body      UpdateWalletSettingsSwagger  true  "Settings payload"
// @Success      200  {object}  WalletSettingsResponse
// @Failure      400  {object}  ErrorResponse  "no settings to update"
// @Failure      403  {object}  ErrorResponse
// @Failure      422  {object}  WalletSettingsFieldError  "invalid or unknown field"
// @Router       /v1/wallets/{walletId}/settings [patch]
func UpdateWalletSettings(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if errResp := authorize(ctx, "wallet.update", map[string]any{"wallet_id": wallet.ID}); errResp != nil {
		return errResp
	}

	body, err := readSettingsBody(ctx)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}
	update, err := walletsettings.Parse(body)
	if err != nil {
		return walletSettingsErrorResponse(ctx, err)
	}
	chainEntity, err := container.Get().ChainRepo.FindByID(wallet.Chain)
	if err != nil || chainEntity == nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": "chain not found"})
	}
	columns, err := update.Columns(wallet, chainEntity.AdapterType)
	if err != nil {
		return walletSettingsErrorResponse(ctx, err)
	}
	if err := container.Get().WalletRepo.UpdateFields(wallet.ID, columns); err != nil {
		return MapInternalError(ctx, err, "update_wallet_settings")
	}
	updated, err := container.Get().WalletRepo.FindByID(wallet.ID)
	if err != nil || updated == nil {
		return MapInternalError(ctx, fmt.Errorf("reload wallet %s: %v", wallet.ID, err), "reload_wallet_settings")
	}
	auditWalletSettingsChange(ctx, wallet, updated, columns)
	return ctx.Response().Json(http.StatusOK, walletSettingsResponse(updated))
}

func readSettingsBody(ctx http.Context) ([]byte, error) {
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, errors.New("request body is required")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, walletsettings.MaxBodyBytes+1))
	if err != nil {
		return nil, errors.New("request body could not be read")
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func walletSettingsErrorResponse(ctx http.Context, err error) http.Response {
	if errors.Is(err, walletsettings.ErrNoFields) {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}
	var fieldErr *walletsettings.FieldError
	if errors.As(err, &fieldErr) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": fieldErr.Error(),
			"field": fieldErr.Field,
		})
	}
	return MapInternalError(ctx, err, "wallet_settings")
}

// auditWalletSettingsChange logs who changed which settings, with old and new values.
func auditWalletSettingsChange(ctx http.Context, before, after *models.Wallet, columns map[string]any) {
	changed := make([]string, 0, len(columns))
	for column := range columns {
		changed = append(changed, column)
	}
	sort.Strings(changed)
	userID, _ := ctx.Value("user_id").(uuid.UUID)
	slog.Info("wallet settings updated",
		"wallet_id", before.ID,
		"chain", before.Chain,
		"user_id", userID,
		"fields", strings.Join(changed, ","),
		"fee_multiplier_before", nullDecimalText(before.FeeMultiplier),
		"fee_multiplier_after", nullDecimalText(after.FeeMultiplier),
		"fee_rate_min_before", intPointerText(before.FeeRateMin),
		"fee_rate_min_after", intPointerText(after.FeeRateMin),
		"fee_rate_max_before", intPointerText(before.FeeRateMax),
		"fee_rate_max_after", intPointerText(after.FeeRateMax),
	)
}

func nullDecimalText(value numeric.NullDecimal) string {
	if !value.Valid {
		return "null"
	}
	return value.Decimal.String()
}

func intPointerText(value *int) string {
	if value == nil {
		return "null"
	}
	return strconv.Itoa(*value)
}

func walletSettingsResponse(wallet *models.Wallet) WalletSettingsResponse {
	return WalletSettingsResponse{
		Label:             wallet.Label,
		FeeRateMin:        wallet.FeeRateMin,
		FeeRateMax:        wallet.FeeRateMax,
		FeeMultiplier:     wallet.FeeMultiplier,
		RequiredApprovals: wallet.RequiredApprovals,
		FrozenUntil:       wallet.FrozenUntil,
		Status:            wallet.Status,
	}
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
func FreezeWallet(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)
	if errResp := authorize(ctx, "wallet.freeze", map[string]any{"wallet_id": wallet.ID}); errResp != nil {
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

	if err := container.Get().WalletRepo.UpdateField(wallet.ID, "frozen_until", frozenUntil); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to freeze wallet"})
	}
	if err := container.Get().WalletRepo.UpdateField(wallet.ID, "status", "frozen"); err != nil {
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

// UpdateWalletSettingsSwagger lists the accepted fields; omit a field to keep
// it, send null to reset it.
type UpdateWalletSettingsSwagger struct {
	Label             *string  `json:"label,omitempty" example:"Treasury"`
	FeeRateMin        *int     `json:"fee_rate_min,omitempty" minimum:"1" maximum:"10000" example:"2"`
	FeeRateMax        *int     `json:"fee_rate_max,omitempty" minimum:"1" maximum:"10000" example:"50"`
	FeeMultiplier     *float64 `json:"fee_multiplier,omitempty" minimum:"1" maximum:"5" example:"1.25"`
	RequiredApprovals *int     `json:"required_approvals,omitempty" minimum:"1" maximum:"10" example:"1"`
}

type WalletSettingsFieldError struct {
	Error string `json:"error" example:"fee_multiplier: must be between 1.00 and 5.00 with at most 4 decimal places"`
	Field string `json:"field" example:"fee_multiplier"`
}

type FreezeWalletSwagger struct {
	FrozenUntil *time.Time `json:"frozen_until,omitempty"`
}

type WalletSettingsResponse struct {
	Label             string              `json:"label"`
	FeeRateMin        *int                `json:"fee_rate_min"`
	FeeRateMax        *int                `json:"fee_rate_max"`
	FeeMultiplier     numeric.NullDecimal `json:"fee_multiplier" swaggertype:"number" example:"1.25"`
	RequiredApprovals int                 `json:"required_approvals"`
	FrozenUntil       *time.Time          `json:"frozen_until"`
	Status            string              `json:"status"`
}
