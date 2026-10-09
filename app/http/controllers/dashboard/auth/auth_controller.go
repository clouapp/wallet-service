package auth

import (
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	authrequests "github.com/macrowallets/waas/app/http/requests/dashboard/auth"
	resources "github.com/macrowallets/waas/app/http/resources/dashboard/auth"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// AuthController signs dashboard users in and out: registration, the
// password login and its 2FA step, the session refresh and the logout. The
// session JWT is signed with the request's own guard.
type AuthController struct {
	signIn *authsvc.SignIn
}

// NewAuthController wires the auth handlers to the sign-in flows.
func NewAuthController(signIn *authsvc.SignIn) *AuthController {
	if signIn == nil {
		panic("dashboard auth controller: sign in is required")
	}
	return &AuthController{signIn: signIn}
}

// Register godoc
//
//	@Summary		Register a new user
//	@Description	Creates a new user account and sends a welcome email
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		RegisterSwagger		true	"Registration payload"
//	@Success		201		{object}	AuthResponse
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		422		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/register [post]
func (c *AuthController) Register(ctx http.Context) http.Response {
	var req authrequests.RegisterRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	registered, err := c.signIn.Register(ctx.Context(), appfacades.Auth(ctx), authsvc.RegisterInput{
		Email:            req.Email,
		Password:         req.Password,
		FullName:         req.FullName,
		OrganizationName: req.OrganizationName,
	})
	if err != nil {
		return mapError(ctx, err, "failed to create user")
	}

	return ctx.Response().Status(http.StatusCreated).Json(resources.NewRegistered(registered))
}

// Login godoc
//
//	@Summary		Authenticate a user
//	@Description	Validates credentials and returns JWT access + refresh tokens. If TOTP is enabled, returns a challenge token requiring 2FA.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		LoginSwagger		true	"Login credentials"
//	@Success		200		{object}	AuthResponse
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/login [post]
func (c *AuthController) Login(ctx http.Context) http.Response {
	var req authrequests.LoginRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	signedIn, err := c.signIn.Login(ctx.Context(), appfacades.Auth(ctx), authsvc.LoginInput{Email: req.Email, Password: req.Password})
	if err != nil {
		return mapError(ctx, err, "failed to create session")
	}

	if signedIn.Challenge != nil {
		return ctx.Response().Success().Json(resources.NewTwoFactorChallenge(*signedIn.Challenge))
	}
	return ctx.Response().Success().Json(resources.NewSignedIn(signedIn))
}

// Verify godoc
//
//	@Summary		Complete 2FA login
//	@Description	Validates a TOTP code or recovery code and returns full JWT tokens
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		TwoFactorSwagger	true	"2FA verification payload"
//	@Success		200		{object}	AuthResponse
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/2fa/verify [post]
func (c *AuthController) Verify(ctx http.Context) http.Response {
	var req authrequests.VerifyTwoFactorRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	signedIn, err := c.signIn.Verify(ctx.Context(), appfacades.Auth(ctx), authsvc.VerifyInput{
		ChallengeToken: req.ChallengeToken,
		Code:           req.Code,
		RecoveryCode:   req.RecoveryCode,
	})
	if err != nil {
		return mapError(ctx, err, "internal error")
	}

	return ctx.Response().Success().Json(resources.NewSignedIn(signedIn))
}

// Refresh godoc
//
//	@Summary		Refresh access token
//	@Description	Exchanges a valid refresh token for a new access + refresh token pair
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			request	body		RefreshTokenSwagger	true	"Refresh token"
//	@Success		200		{object}	AuthResponse
//	@Failure		400		{object}	responses.ErrorBody
//	@Failure		401		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/auth/refresh [post]
func (c *AuthController) Refresh(ctx http.Context) http.Response {
	var req authrequests.RefreshTokenRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	tokens, err := c.signIn.Refresh(ctx.Context(), appfacades.Auth(ctx), req.RefreshToken)
	if err != nil {
		return mapRefreshError(ctx, err)
	}

	return ctx.Response().Success().Json(resources.NewSession(tokens))
}

// Logout godoc
//
//	@Summary		Logout current user
//	@Description	Ends every session of the user: access tokens and refresh tokens
//	@Tags			Auth
//	@Security		BearerAuth
//	@Produce		json
//	@Success		204	"No content"
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		500	{object}	responses.ErrorBody
//	@Router			/auth/logout [post]
func (c *AuthController) Logout(ctx http.Context) http.Response {
	userID := requestctx.MustUserID(ctx)

	if err := c.signIn.Logout(ctx.Context(), userID); err != nil {
		return mapError(ctx, err, "failed to end session")
	}

	return ctx.Response().NoContent()
}
