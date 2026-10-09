package wallets

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

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/walletsettings"
	"github.com/macrowallets/waas/pkg/numeric"
)

// SettingsController serves the dashboard wallet settings and freeze routes.
type SettingsController struct {
	wallets     *walletrecords.Wallets
	memberships *walletrecords.Memberships
	chains      *chainsvc.Service
}

// WalletSettingsControllerDeps is everything the dashboard wallet settings controller needs.
// Every field is required.
type WalletSettingsControllerDeps struct {
	Wallets     *walletrecords.Wallets
	Memberships *walletrecords.Memberships
	Chains      *chainsvc.Service
}

// NewSettingsController wires the dashboard wallet settings handlers from WalletSettingsControllerDeps.
func NewSettingsController(deps WalletSettingsControllerDeps) *SettingsController {
	if deps.Wallets == nil {
		panic("dashboard wallet settings controller: wallets service is required")
	}
	if deps.Memberships == nil {
		panic("dashboard wallet settings controller: wallet memberships are required")
	}
	if deps.Chains == nil {
		panic("dashboard wallet settings controller: chains service is required")
	}
	return &SettingsController{
		wallets:     deps.Wallets,
		memberships: deps.Memberships,
		chains:      deps.Chains,
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
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/settings [get]
func (ctrl *SettingsController) GetWalletSettings(ctx http.Context) http.Response {
	return walletSettingsJSON(ctx, requestctx.MustWallet(ctx))
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
// @Failure      400  {object}  responses.ErrorBody  "no settings to update"
// @Failure      403  {object}  responses.ErrorBody
// @Failure      422  {object}  responses.ErrorBody  "invalid or unknown field"
// @Router       /wallets/{walletId}/settings [patch]
func (ctrl *SettingsController) UpdateWalletSettings(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if errResp := controllers.Deny(ctx, policies.WalletUpdate(controllers.WalletMembership(ctx, ctrl.memberships, wallet.ID))); errResp != nil {
		return errResp
	}

	body, err := readSettingsBody(ctx)
	if err != nil {
		return settingsBodyError(ctx, err)
	}
	update, err := walletsettings.Parse(body)
	if err != nil {
		return walletSettingsErrorResponse(ctx, err)
	}
	adapterType, errResp := ctrl.settingsAdapterType(ctx, wallet.Chain, update)
	if errResp != nil {
		return errResp
	}
	columns, err := update.Columns(wallet, adapterType)
	if err != nil {
		return walletSettingsErrorResponse(ctx, err)
	}
	if err := ctrl.wallets.UpdateSettings(ctx.Context(), wallet.ID, columns); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update wallet settings")
	}
	updated, err := ctrl.wallets.FindByID(ctx.Context(), wallet.ID)
	if err != nil || updated == nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update wallet settings")
	}
	auditWalletSettingsChange(ctx, wallet, updated, columns)
	return walletSettingsJSON(ctx, updated)
}

// settingsAdapterType loads the chain only when a fee field is present. A label
// or approval change does not need a chain row.
func (ctrl *SettingsController) settingsAdapterType(ctx http.Context, chainID string, update walletsettings.Update) (string, http.Response) {
	if !update.FeeMultiplier.Set && !update.FeeRateMin.Set && !update.FeeRateMax.Set {
		return "", nil
	}
	chainEntity, err := ctrl.chains.FindByID(ctx.Context(), chainID)
	if err != nil || chainEntity == nil {
		return "", responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, "chain not found")
	}
	return chainEntity.AdapterType, nil
}

var (
	errSettingsBodyRequired   = errors.New("request body is required")
	errSettingsBodyUnreadable = errors.New("request body could not be read")
)

func readSettingsBody(ctx http.Context) ([]byte, error) {
	request := ctx.Request().Origin()
	if request == nil || request.Body == nil {
		return nil, errSettingsBodyRequired
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, walletsettings.MaxBodyBytes+1))
	if err != nil {
		return nil, errSettingsBodyUnreadable
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func walletSettingsErrorResponse(ctx http.Context, err error) http.Response {
	if errors.Is(err, walletsettings.ErrNoFields) {
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, walletsettings.ErrNoFields.Error())
	}
	var fieldErr *walletsettings.FieldError
	if errors.As(err, &fieldErr) {
		return responses.FieldsFailed(ctx, map[string][]string{fieldErr.Field: {fieldErr.Message}})
	}
	return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update wallet settings")
}

func settingsBodyError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, errSettingsBodyRequired):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, errSettingsBodyRequired.Error())
	case errors.Is(err, errSettingsBodyUnreadable):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, errSettingsBodyUnreadable.Error())
	default:
		slog.Error("read wallet settings body failed", "error_type", fmt.Sprintf("%T", err))
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
}

func auditWalletSettingsChange(ctx http.Context, before, after *models.Wallet, columns map[string]any) {
	changed := make([]string, 0, len(columns))
	for column := range columns {
		changed = append(changed, column)
	}
	sort.Strings(changed)
	userID, _ := requestctx.UserID(ctx)
	slog.Info("wallet settings updated for wallet "+before.ID.String()+" by user "+userID.String(),
		"chain", before.Chain,
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

func walletSettingsJSON(ctx http.Context, wallet *models.Wallet) http.Response {
	return ctx.Response().Success().Json(WalletSettingsResponse{
		Label:             wallet.Label,
		FeeRateMin:        wallet.FeeRateMin,
		FeeRateMax:        wallet.FeeRateMax,
		FeeMultiplier:     wallet.FeeMultiplier,
		RequiredApprovals: wallet.RequiredApprovals,
		FrozenUntil:       wallet.FrozenUntil,
		Status:            wallet.Status,
	})
}

// The archive route documents the wallet body resource.
var _ walletresource.Wallet

// ArchiveWallet godoc
// @Summary      Archive a wallet
// @Description  Sets wallet status to archived. Requires a wallet or account owner/admin. Archiving an archived wallet is rejected.
// @Tags         Wallet Settings
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  walletresource.Wallet
// @Failure      403  {object}  responses.ErrorBody
// @Failure      409  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/archive [post]
func (ctrl *SettingsController) ArchiveWallet(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if wallet.Status == models.WalletStatusArchived {
		return responses.Fail(ctx, http.StatusConflict, responses.CodeConflict, "wallet already archived")
	}
	if err := ctrl.wallets.SetStatus(ctx.Context(), wallet.ID, models.WalletStatusArchived); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to archive wallet")
	}
	wallet.Status = models.WalletStatusArchived
	return ctx.Response().Success().Json(walletresource.WithNetworkFrom(wallet, controllers.ResolveWalletChainNetwork(ctx.Context(), ctrl.chains, wallet.Chain)))
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
// @Failure      403  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/freeze [post]
func (ctrl *SettingsController) FreezeWallet(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req requests.FreezeWalletRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	frozenUntil := time.Now().Add(24 * time.Hour)
	if s := strings.TrimSpace(req.FrozenUntil); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return responses.FieldsFailed(ctx, map[string][]string{"frozen_until": {"must be an RFC3339 timestamp"}})
		}
		frozenUntil = parsed
	}

	if err := ctrl.wallets.SetFrozenUntil(ctx.Context(), wallet.ID, frozenUntil); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to freeze wallet")
	}
	if err := ctrl.wallets.SetStatus(ctx.Context(), wallet.ID, "frozen"); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to freeze wallet")
	}
	wallet.FrozenUntil = &frozenUntil
	wallet.Status = "frozen"

	return ctx.Response().Success().Json(http.Json{
		"status":       wallet.Status,
		"frozen_until": wallet.FrozenUntil,
	})
}

// UpdateWalletSettingsSwagger lists the accepted fields; omit a field to keep
// it, send null to reset it.
type UpdateWalletSettingsSwagger struct {
	Label             *string  `json:"label,omitempty" example:"Treasury"`
	FeeRateMin        *int     `json:"fee_rate_min,omitempty" example:"2"`
	FeeRateMax        *int     `json:"fee_rate_max,omitempty" example:"50"`
	FeeMultiplier     *float64 `json:"fee_multiplier,omitempty" example:"1.25"`
	RequiredApprovals *int     `json:"required_approvals,omitempty" example:"1"`
}

type FreezeWalletSwagger struct {
	FrozenUntil *time.Time `json:"frozen_until,omitempty"`
}

type WalletSettingsResponse struct {
	Label             string              `json:"label"`
	FeeRateMin        *int                `json:"fee_rate_min"`
	FeeRateMax        *int                `json:"fee_rate_max"`
	FeeMultiplier     numeric.NullDecimal `json:"fee_multiplier"`
	RequiredApprovals int                 `json:"required_approvals"`
	FrozenUntil       *time.Time          `json:"frozen_until"`
	Status            string              `json:"status"`
}
