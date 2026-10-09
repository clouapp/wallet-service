package users

import (
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// TotpController turns the signed-in user's TOTP second factor on and off.
type TotpController struct {
	totp *authsvc.TOTPEnrollment
}

// NewTotpController wires the TOTP handlers to the enrollment flows, which seal
// and open the secret.
func NewTotpController(totp *authsvc.TOTPEnrollment) *TotpController {
	if totp == nil {
		panic("dashboard totp controller: totp enrollment is required")
	}
	return &TotpController{totp: totp}
}

// Setup godoc
//
//	@Summary		Begin TOTP enrollment
//	@Description	Generates a TOTP secret and QR URL; stores encrypted secret until verified
//	@Tags			User
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	TotpSetupSwagger
//	@Failure		500	{object}	responses.ErrorBody
//	@Router			/users/me/totp/setup [post]
func (c *TotpController) Setup(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	setup, err := c.totp.Setup(ctx.Context(), user)
	if err != nil {
		return mapError(ctx, err, "failed to save TOTP secret")
	}

	return ctx.Response().Success().Json(resources.NewTOTPSecret(setup))
}

// Confirm godoc
//
//	@Summary		Complete TOTP enrollment
//	@Description	Verifies the TOTP code, enables 2FA, and returns one-time recovery codes
//	@Tags			User
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ConfirmTotpSwagger	true	"TOTP verification code"
//	@Success		200		{object}	map[string]interface{}
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		500		{object}	responses.ErrorBody
//	@Router			/users/me/totp/verify [post]
func (c *TotpController) Confirm(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.ConfirmTwoFactorRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	confirmed, err := c.totp.Confirm(ctx.Context(), user, req.Code)
	if err != nil {
		return mapError(ctx, err, "failed to enable 2FA")
	}

	return ctx.Response().Success().Json(resources.NewTOTPConfirmed(confirmed))
}

// Destroy godoc
//
//	@Summary		Disable TOTP
//	@Description	Disables 2FA and clears TOTP secret and recovery codes
//	@Tags			User
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Failure		500	{object}	responses.ErrorBody
//	@Router			/users/me/totp [delete]
//
// The proof (DisableTotpRequest) is read by the service only when the user has
// 2FA on, so the body of a user without it is not read.
func (c *TotpController) Destroy(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	disabled, err := c.totp.Disable(ctx.Context(), appfacades.Auth(ctx), user.ID, usersrequests.NewDisableTotpProof(ctx))
	if err != nil {
		return mapDisableError(ctx, err)
	}

	return ctx.Response().Success().Json(resources.NewTOTPDisabled(disabled))
}
