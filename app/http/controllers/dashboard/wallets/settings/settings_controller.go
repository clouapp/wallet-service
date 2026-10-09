package settings

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	settingsrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/settings"
	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	settingsresources "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/settings"
	"github.com/macrowallets/waas/app/services/walletsettings"
)

// SettingsController serves the dashboard wallet settings, freeze and archive
// routes. Who may update, freeze or archive is the route's Wallet* guard.
type SettingsController struct {
	settings *walletsettings.Service
}

// NewSettingsController wires the controller with the wallet settings.
func NewSettingsController(settings *walletsettings.Service) *SettingsController {
	if settings == nil {
		panic("dashboard wallet settings controller: wallet settings are required")
	}
	return &SettingsController{settings: settings}
}

// Show godoc
//
//	@Summary		Get wallet settings
//	@Description	Returns fee, approval, and freeze settings for a wallet
//	@Tags			Wallet Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	settingsresources.Settings
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/settings [get]
func (c *SettingsController) Show(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	return ctx.Response().Success().Json(settingsresources.NewSettings(wallet))
}

// Update godoc
//
//	@Summary		Update wallet settings
//	@Description	Updates the wallet's name and fee settings. Requires wallet or account owner/admin. Only the listed fields are accepted; each may be omitted (unchanged) or null (reset to the network default). fee_multiplier (1.0000–5.0000, up to 4 decimals) scales the gas price on EVM chains and the fee rate on Bitcoin, in fee estimates and in the withdrawals themselves; it does not apply to Solana. fee_rate_min/fee_rate_max (1–10000 sat/vB, min ≤ max) clamp the Bitcoin fee rate. Freezing uses POST /freeze.
//	@Tags			Wallet Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string						true	"Wallet UUID"
//	@Param			request		body		UpdateWalletSettingsSwagger	true	"Settings payload"
//	@Success		200			{object}	settingsresources.Settings
//	@Failure		400			{object}	responses.ErrorBody	"no settings to update"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody	"invalid or unknown field"
//	@Router			/wallets/{walletId}/settings [patch]
func (c *SettingsController) Update(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	actorID, _ := requestctx.UserID(ctx)

	req, err := settingsrequests.ReadUpdateRequest(ctx)
	if err != nil {
		return mapError(ctx, err, "update wallet settings")
	}

	updated, err := c.settings.Update(ctx.Context(), walletsettings.UpdateInput{Wallet: wallet, Body: req.Body, ActorID: actorID})
	if err != nil {
		return mapError(ctx, err, "update wallet settings")
	}

	return ctx.Response().Success().Json(settingsresources.NewSettings(updated))
}

// Archive godoc
//
//	@Summary		Archive a wallet
//	@Description	Sets wallet status to archived. Requires a wallet or account owner/admin. Archiving an archived wallet is rejected.
//	@Tags			Wallet Settings
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	walletresource.Wallet
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		409			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/archive [post]
func (c *SettingsController) Archive(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	archived, err := c.settings.Archive(ctx.Context(), wallet)
	if err != nil {
		return mapError(ctx, err, "archive wallet")
	}

	return ctx.Response().Success().Json(walletresource.WithNetworkFrom(archived.Wallet, archived.Network))
}

// Freeze godoc
//
//	@Summary		Freeze a wallet
//	@Description	Freezes the wallet until the specified timestamp. Requires wallet or account owner.
//	@Tags			Wallet Settings
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string				true	"Wallet UUID"
//	@Param			request		body		FreezeWalletSwagger	true	"Freeze payload"
//	@Success		200			{object}	settingsresources.Settings
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/freeze [post]
func (c *SettingsController) Freeze(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req settingsrequests.FreezeRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	until, err := c.settings.Freeze(ctx.Context(), wallet.ID, req.Until())
	if err != nil {
		return mapError(ctx, err, "freeze wallet")
	}

	return ctx.Response().Success().Json(settingsresources.NewFrozen(until))
}
