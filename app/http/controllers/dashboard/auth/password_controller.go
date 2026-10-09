package auth

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	authrequests "github.com/macrowallets/waas/app/http/requests/dashboard/auth"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/auth"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// PasswordController recovers a forgotten password: it mails a reset link and
// spends it.
type PasswordController struct {
	users       *usersvc.Service
	credentials *authsvc.Credentials
}

// NewPasswordController wires the recovery handlers: the user service mails
// the link, the credential flows spend it.
func NewPasswordController(users *usersvc.Service, credentials *authsvc.Credentials) *PasswordController {
	if users == nil {
		panic("dashboard password reset controller: users service is required")
	}
	if credentials == nil {
		panic("dashboard password reset controller: credentials are required")
	}
	return &PasswordController{users: users, credentials: credentials}
}

// Forgot godoc
//
//	@Summary		Request password reset email
//	@Description	Sends a password reset link to the user's email if the address is registered
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ForgotPasswordSwagger	true	"Email address"
//	@Success		200		{object}	map[string]string
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/recover [post]
func (c *PasswordController) Forgot(ctx http.Context) http.Response {
	var req authrequests.ForgotPasswordRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	c.users.ForgotPassword(ctx.Context(), req.Email)

	return ctx.Response().Success().Json(resources.NewResetRequested())
}

// Reset godoc
//
//	@Summary		Reset password using token
//	@Description	Validates the reset token and updates the user's password
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ResetPasswordSwagger	true	"Token and new password"
//	@Success		200		{object}	map[string]string
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/recover/confirm [post]
func (c *PasswordController) Reset(ctx http.Context) http.Response {
	var req authrequests.ResetPasswordRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	if err := c.credentials.ResetPassword(ctx.Context(), req.Token, req.NewPassword); err != nil {
		return mapError(ctx, err, "failed to update password")
	}

	return ctx.Response().Success().Json(resources.NewPasswordReset())
}
