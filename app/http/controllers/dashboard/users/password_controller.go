package users

import (
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	usersrequests "github.com/macrowallets/waas/app/http/requests/dashboard/users"
	authresources "github.com/macrowallets/waas/app/http/resources/dashboard/auth"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// PasswordController changes the signed-in user's password.
type PasswordController struct {
	credentials *authsvc.Credentials
}

// NewPasswordController wires the password handler to the credential flows.
func NewPasswordController(credentials *authsvc.Credentials) *PasswordController {
	if credentials == nil {
		panic("dashboard password controller: credentials are required")
	}
	return &PasswordController{credentials: credentials}
}

// Update godoc
//
//	@Summary		Change password
//	@Description	Validates the current password and updates it
//	@Tags			User
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ChangePasswordSwagger	true	"Password change payload"
//	@Success		200		{object}	map[string]string
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Router			/users/me/password [post]
func (c *PasswordController) Update(ctx http.Context) http.Response {
	user := requestctx.MustUser(ctx)

	var req usersrequests.ChangePasswordRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	tokens, err := c.credentials.ChangePassword(ctx.Context(), appfacades.Auth(ctx), user, req.CurrentPassword, req.NewPassword)
	if err != nil {
		return mapPasswordError(ctx, err)
	}

	return ctx.Response().Success().Json(authresources.NewPasswordChanged(tokens))
}
