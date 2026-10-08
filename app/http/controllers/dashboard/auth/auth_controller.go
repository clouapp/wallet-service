package auth

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	userresource "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

type AuthController struct {
	users          *usersvc.Service
	accounts       *accountsvc.Service
	refreshTokens  *sessions.RefreshTokens
	passwordResets *sessions.PasswordResets
	passwords      *authsvc.Service
	twoFactor      *authsvc.TwoFactorLogin
	revoker        *authsvc.SessionRevoker
	credentialMail *credentialmail.Service
}

// AuthControllerDeps is everything the dashboard auth controller needs.
// Every field is required.
type AuthControllerDeps struct {
	Users          *usersvc.Service
	Accounts       *accountsvc.Service
	RefreshTokens  *sessions.RefreshTokens
	PasswordResets *sessions.PasswordResets
	Passwords      *authsvc.Service
	TwoFactor      *authsvc.TwoFactorLogin
	Revoker        *authsvc.SessionRevoker
	CredentialMail *credentialmail.Service
}

// NewAuthController wires the dashboard auth handlers from AuthControllerDeps.
// Every dependency is a provider singleton, resolved once when the route table is built.
func NewAuthController(deps AuthControllerDeps) *AuthController {
	if deps.Users == nil {
		panic("dashboard auth controller: users service is required")
	}
	if deps.Accounts == nil {
		panic("dashboard auth controller: account service is required")
	}
	if deps.RefreshTokens == nil {
		panic("dashboard auth controller: refresh token service is required")
	}
	if deps.PasswordResets == nil {
		panic("dashboard auth controller: password reset service is required")
	}
	if deps.Passwords == nil {
		panic("dashboard auth controller: auth service is required")
	}
	if deps.TwoFactor == nil {
		panic("dashboard auth controller: two factor login is required")
	}
	if deps.Revoker == nil {
		panic("dashboard auth controller: session revoker is required")
	}
	if deps.CredentialMail == nil {
		panic("dashboard auth controller: credential mail is required")
	}
	return &AuthController{
		users:          deps.Users,
		accounts:       deps.Accounts,
		refreshTokens:  deps.RefreshTokens,
		passwordResets: deps.PasswordResets,
		passwords:      deps.Passwords,
		twoFactor:      deps.TwoFactor,
		revoker:        deps.Revoker,
		credentialMail: deps.CredentialMail,
	}
}

func (ctrl *AuthController) sessions() controllers.SessionIssuer {
	return controllers.SessionIssuer{Passwords: ctrl.passwords, Refresh: ctrl.refreshTokens, Revoker: ctrl.revoker}
}

// Register godoc
// @Summary      Register a new user
// @Description  Creates a new user account and sends a welcome email
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      RegisterSwagger  true  "Registration payload"
// @Success      201      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      422      {object}  ErrorResponse
// @Router       /auth/register [post]
func (ctrl *AuthController) Register(ctx http.Context) http.Response {
	var req requests.RegisterRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	hash, err := ctrl.passwords.HashPassword(req.Password)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to hash password")
	}

	user, err := ctrl.accounts.Onboard(ctx.Context(), accountsvc.OnboardInput{
		Email:            req.Email,
		PasswordHash:     hash,
		FullName:         req.FullName,
		OrganizationName: req.OrganizationName,
	}, func(userID uuid.UUID) error {
		if mailErr := ctrl.credentialMail.SendWelcome(ctx.Context(), userID); mailErr != nil {
			appfacades.Log().WithContext(ctx).Error("auth: send welcome mail failed")
		}
		return nil
	})
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create user")
	}

	accounts, defaultAccount, accountsErr := ctrl.loadUserAccounts(user)
	if accountsErr != nil {
		return membershipReadUnavailable(ctx)
	}

	accessToken, err := appfacades.Auth(ctx).LoginUsingID(user.ID.String())
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: login after register: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}

	resp := http.Json{
		"access_token": accessToken,
		"user":         userresource.UserFrom(user),
		"accounts":     accounts,
	}
	if defaultAccount != nil {
		resp["account_id"] = defaultAccount["id"]
		resp["account"] = defaultAccount
	}
	return ctx.Response().Status(http.StatusCreated).Json(resp)
}

// Login godoc
// @Summary      Authenticate a user
// @Description  Validates credentials and returns JWT access + refresh tokens. If TOTP is enabled, returns a challenge token requiring 2FA.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      LoginSwagger  true  "Login credentials"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/login [post]
func (ctrl *AuthController) Login(ctx http.Context) http.Response {
	var req requests.LoginRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	userPtr, err := ctrl.users.FindByEmail(ctx.Context(), req.Email)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		appfacades.Log().WithContext(ctx).Errorf("auth: login: find user: %v", err)
		return responses.InternalError(ctx, nil)
	}
	if userPtr == nil {
		// Spend the bcrypt time a wrong password would, so the response time
		// does not tell a registered email from an unknown one.
		ctrl.passwords.CheckPassword(req.Password, authsvc.DummyPasswordHash)
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid credentials")
	}
	user := *userPtr

	if !ctrl.passwords.CheckPassword(req.Password, user.PasswordHash) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid credentials")
	}
	if !policies.UserMayHoldSession(user.Status) {
		return controllers.InactiveUserResponse(ctx)
	}
	if policies.UserIsSuspended(user.SuspendedAt) {
		return responses.SuspendedUser(ctx)
	}

	if user.TotpEnabled {
		if err := ctrl.revoker.AwaitIssuable(user.SessionsRevokedAt); err != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: begin 2fa: %v", err)
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
		}
		challenge, err := ctrl.twoFactor.Begin(&user)
		if err != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: begin 2fa: %v", err)
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
		}
		return ctx.Response().Success().Json(http.Json{
			"requires_2fa":    true,
			"challenge_token": challenge.Token,
			"expires_in":      int(challenge.ExpiresIn.Seconds()),
		})
	}

	accounts, defaultAccount, accountsErr := ctrl.loadUserAccounts(&user)
	if accountsErr != nil {
		return membershipReadUnavailable(ctx)
	}

	tokens, err := ctrl.sessions().IssueSession(ctx, user.ID, user.SessionsRevokedAt)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: login: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}
	return ctx.Response().Success().Json(ctrl.signedInResponse(&user, tokens, accounts, defaultAccount))
}

// VerifyTwoFactor godoc
// @Summary      Complete 2FA login
// @Description  Validates a TOTP code or recovery code and returns full JWT tokens
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      TwoFactorSwagger  true  "2FA verification payload"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/2fa/verify [post]
func (ctrl *AuthController) VerifyTwoFactor(ctx http.Context) http.Response {
	var req requests.VerifyTwoFactorRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	if req.Code == "" && req.RecoveryCode == "" {
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, "code or recovery_code is required")
	}

	user, err := ctrl.twoFactor.Complete(req.ChallengeToken, req.Code, req.RecoveryCode)
	if err != nil {
		return controllers.TwoFactorErrorResponse(ctx, err)
	}
	if !policies.UserMayHoldSession(user.Status) {
		return controllers.InactiveUserResponse(ctx)
	}
	if policies.UserIsSuspended(user.SuspendedAt) {
		return responses.SuspendedUser(ctx)
	}

	accounts, defaultAccount, accountsErr := ctrl.loadUserAccounts(user)
	if accountsErr != nil {
		return membershipReadUnavailable(ctx)
	}

	tokens, err := ctrl.sessions().IssueSession(ctx, user.ID, user.SessionsRevokedAt)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: 2fa login: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}
	return ctx.Response().Success().Json(ctrl.signedInResponse(user, tokens, accounts, defaultAccount))
}

// RefreshToken godoc
// @Summary      Refresh access token
// @Description  Exchanges a valid refresh token for a new access + refresh token pair
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      RefreshTokenSwagger  true  "Refresh token"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/refresh [post]
func (ctrl *AuthController) RefreshToken(ctx http.Context) http.Response {
	var req requests.RefreshTokenRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	tokens, tokErr := ctrl.refreshTokens.FindValidTokens(ctx.Context())
	if tokErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: find refresh tokens: %v", tokErr)
	}

	var matched *models.RefreshToken
	for i := range tokens {
		if ctrl.passwords.CheckToken(req.RefreshToken, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired refresh token")
	}

	rotated, err := ctrl.refreshTokens.RevokeIfActive(ctx.Context(), matched.ID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: revoke refresh token: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}
	if !rotated {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired refresh token")
	}

	owner, err := ctrl.users.FindByID(ctx.Context(), matched.UserID)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: refresh: load user: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}
	if owner == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired refresh token")
	}
	if !policies.UserMayHoldSession(owner.Status) {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "user is not active")
	}
	if policies.UserIsSuspended(owner.SuspendedAt) {
		return responses.SuspendedUser(ctx)
	}

	session, err := ctrl.sessions().IssueSession(ctx, owner.ID, owner.SessionsRevokedAt)
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: refresh: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create session")
	}
	return ctx.Response().Success().Json(http.Json{
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	})
}

// Logout godoc
// @Summary      Logout current user
// @Description  Ends every session of the user: access tokens and refresh tokens
// @Tags         Auth
// @Security     BearerAuth
// @Produce      json
// @Success      204  "No content"
// @Failure      401  {object}  ErrorResponse
// @Router       /auth/logout [post]
func (ctrl *AuthController) Logout(ctx http.Context) http.Response {
	// The session watermark, not the guard's per-token blacklist: a JWT carries
	// only whole-second claims, so a sign-in in the second of the logout would
	// mint the very token the blacklist just refused (see session_revocation.go).
	if uid, ok := requestctx.UserID(ctx); ok {
		if _, err := ctrl.revoker.RevokeAll(ctx.Context(), uid); err != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: logout: revoke sessions: %v", err)
			return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to end session")
		}
	}
	return ctx.Response().NoContent()
}

// ForgotPassword godoc
// @Summary      Request password reset email
// @Description  Sends a password reset link to the user's email if the address is registered
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      ForgotPasswordSwagger  true  "Email address"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  ErrorResponse
// @Router       /auth/forgot-password [post]
func (ctrl *AuthController) ForgotPassword(ctx http.Context) http.Response {
	var req requests.ForgotPasswordRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	if err := ctrl.users.RequestPasswordReset(ctx.Context(), req.Email); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: send password reset mail failed")
	}

	return ctx.Response().Success().Json(http.Json{"message": "if that address is registered, you will receive a reset link"})
}

// ResetPassword godoc
// @Summary      Reset password using token
// @Description  Validates the reset token and updates the user's password
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      ResetPasswordSwagger  true  "Token and new password"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/reset-password [post]
func (ctrl *AuthController) ResetPassword(ctx http.Context) http.Response {
	var req requests.ResetPasswordRequest
	if errResp := requests.Validate(ctx, &req); errResp != nil {
		return errResp
	}

	tokens, tokErr := ctrl.passwordResets.FindValidTokens(ctx.Context())
	if tokErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: find reset tokens: %v", tokErr)
	}

	var matched *models.PasswordResetToken
	for i := range tokens {
		if ctrl.passwords.CheckToken(req.Token, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired token")
	}

	hash, err := ctrl.passwords.HashPassword(req.NewPassword)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to hash password")
	}

	if err := ctrl.users.UpdatePasswordHash(ctx.Context(), matched.UserID, hash); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: update password: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to update password")
	}

	if err := ctrl.passwordResets.MarkUsed(ctx.Context(), matched.ID); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: mark reset token used: %v", err)
	}
	if _, err := ctrl.revoker.RevokeAll(ctx.Context(), matched.UserID); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: reset password: revoke sessions: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "password reset but existing sessions could not be revoked")
	}

	return ctx.Response().Success().Json(http.Json{"message": "password reset successfully"})
}

func (ctrl *AuthController) signedInResponse(user *models.User, tokens controllers.SessionTokens, accounts []map[string]interface{}, defaultAccount map[string]interface{}) http.Json {
	user.TotpSecret = ""
	resp := http.Json{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"user":          userresource.UserFrom(user),
		"accounts":      accounts,
	}
	if defaultAccount != nil {
		resp["account_id"] = defaultAccount["id"]
		resp["account"] = defaultAccount
	}
	return resp
}

func membershipReadUnavailable(ctx http.Context) http.Response {
	return responses.Fail(ctx, http.StatusServiceUnavailable, responses.CodeUnavailable, "failed to load accounts")
}

func (ctrl *AuthController) loadUserAccounts(user *models.User) ([]map[string]interface{}, map[string]interface{}, error) {
	memberships, err := ctrl.accounts.ListMemberships(context.Background(), user.ID)
	if err != nil {
		appfacades.Log().Errorf("auth: load memberships: %v", err)
		return nil, nil, err
	}

	var accounts []map[string]interface{}
	var defaultAccount map[string]interface{}

	for _, m := range memberships {
		acct, err := ctrl.accounts.FindByID(context.Background(), m.AccountID)
		if err != nil || acct == nil {
			continue
		}
		entry := map[string]interface{}{
			"id":                acct.ID,
			"name":              acct.Name,
			"environment":       acct.Environment,
			"linked_account_id": acct.LinkedAccountID,
			"status":            acct.Status,
			"role":              m.Role,
		}
		accounts = append(accounts, entry)

		if user.DefaultAccountID != nil && *user.DefaultAccountID == acct.ID {
			defaultAccount = entry
		}
	}

	// FindByUserID has no order, so Postgres can return the two onboarded
	// accounts either way. The list is part of the success body.
	sort.Slice(accounts, func(i, j int) bool {
		leftEnvironment, _ := accounts[i]["environment"].(string)
		rightEnvironment, _ := accounts[j]["environment"].(string)
		if leftEnvironment != rightEnvironment {
			return leftEnvironment < rightEnvironment
		}
		leftID, _ := accounts[i]["id"].(uuid.UUID)
		rightID, _ := accounts[j]["id"].(uuid.UUID)
		return leftID.String() < rightID.String()
	})

	if defaultAccount == nil && len(accounts) > 0 {
		defaultAccount = accounts[0]
	}
	return accounts, defaultAccount, nil
}

// ---- Swagger-only types ----

type RegisterSwagger struct {
	Email            string `json:"email" example:"user@example.com"`
	Password         string `json:"password" example:"s3cr3t"`
	FullName         string `json:"full_name" example:"Alice Smith"`
	OrganizationName string `json:"organization_name" example:"Acme Corp"`
}

type LoginSwagger struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"s3cr3t"`
}

type TwoFactorSwagger struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code" example:"123456"`
	RecoveryCode   string `json:"recovery_code" example:"ABCDEFGH12345678"`
}

type RefreshTokenSwagger struct {
	RefreshToken string `json:"refresh_token"`
}

type ForgotPasswordSwagger struct {
	Email string `json:"email" example:"user@example.com"`
}

type ResetPasswordSwagger struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password" example:"newS3cr3t"`
}

type AuthResponse struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	User         userresource.User `json:"user,omitempty"`
}
