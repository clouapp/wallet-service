package auth

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	mails "github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

type AuthController struct {
	users          *usersvc.Service
	accounts       *accountsvc.Service
	refreshTokens  *sessions.RefreshTokens
	passwordResets *sessions.PasswordResets
	passwords      *authsvc.Service
}

// NewAuthController wires the dashboard auth handlers. Every dependency is a
// provider singleton, resolved once when the route table is built.
func NewAuthController(
	users *usersvc.Service,
	accounts *accountsvc.Service,
	refreshTokens *sessions.RefreshTokens,
	passwordResets *sessions.PasswordResets,
	passwords *authsvc.Service,
) *AuthController {
	if users == nil {
		panic("dashboard auth controller: users service is required")
	}
	if accounts == nil {
		panic("dashboard auth controller: account service is required")
	}
	if refreshTokens == nil {
		panic("dashboard auth controller: refresh token service is required")
	}
	if passwordResets == nil {
		panic("dashboard auth controller: password reset service is required")
	}
	if passwords == nil {
		panic("dashboard auth controller: auth service is required")
	}
	return &AuthController{
		users:          users,
		accounts:       accounts,
		refreshTokens:  refreshTokens,
		passwordResets: passwordResets,
		passwords:      passwords,
	}
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	hash, err := ctrl.passwords.HashPassword(req.Password)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	user := &models.User{
		ID:           uuid.New(),
		Email:        req.Email,
		PasswordHash: hash,
		FullName:     req.FullName,
		Status:       "active",
	}
	if err := ctrl.users.Create(ctx.Context(), user); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create user"})
	}

	prodAccountID := uuid.New()
	testAccountID := uuid.New()

	// The link is a foreign key, so the production row cannot point at the
	// test row until that row exists. Create production first, then test,
	// then point production back.
	prodAccount := &models.Account{
		ID:          prodAccountID,
		Name:        req.OrganizationName,
		Status:      "active",
		Environment: models.EnvironmentProd,
	}
	testAccount := &models.Account{
		ID:              testAccountID,
		Name:            req.OrganizationName + " [test]",
		Status:          "active",
		Environment:     models.EnvironmentTest,
		LinkedAccountID: &prodAccountID,
	}
	if err := ctrl.accounts.InsertAccount(ctx.Context(), prodAccount); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create production account"})
	}
	if err := ctrl.accounts.InsertAccount(ctx.Context(), testAccount); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create test account"})
	}
	if err := ctrl.accounts.LinkAccount(ctx.Context(), prodAccountID, testAccountID); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to link accounts"})
	}
	prodAccount.LinkedAccountID = &testAccountID

	prodMembership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: prodAccountID,
		UserID:    user.ID,
		Role:      "owner",
		Status:    "active",
	}
	testMembership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: testAccountID,
		UserID:    user.ID,
		Role:      "owner",
		Status:    "active",
	}
	if err := ctrl.accounts.InsertMembership(ctx.Context(), prodMembership); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: create prod membership: %v", err)
	}
	if err := ctrl.accounts.InsertMembership(ctx.Context(), testMembership); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: create test membership: %v", err)
	}

	user.DefaultAccountID = &prodAccountID
	if err := ctrl.users.UpdateDefaultAccountID(ctx.Context(), user.ID, &prodAccountID); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: set default account: %v", err)
	}

	if err := appfacades.Mail().To([]string{user.Email}).Send(&mails.WelcomeMail{To: user.Email, FullName: user.FullName}); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: send welcome mail: %v", err)
	}

	accessToken, err := appfacades.Auth(ctx).LoginUsingID(user.ID.String())
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: login after register: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}

	accounts, defaultAccount := ctrl.loadUserAccounts(user)

	resp := http.Json{
		"access_token": accessToken,
		"user":         user,
		"accounts":     accounts,
	}
	if defaultAccount != nil {
		resp["account_id"] = defaultAccount["id"]
		resp["account"] = defaultAccount
	}
	return ctx.Response().Json(http.StatusCreated, resp)
}

// Login godoc
// @Summary      Authenticate a user
// @Description  Validates credentials and returns JWT access + refresh tokens. If TOTP is enabled, returns a partial token requiring 2FA.
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	userPtr, err := ctrl.users.FindByEmail(ctx.Context(), req.Email)
	if err != nil || userPtr == nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}
	user := *userPtr

	if !ctrl.passwords.CheckPassword(req.Password, user.PasswordHash) {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}

	if user.TotpEnabled {
		partialToken, err := appfacades.Auth(ctx).LoginUsingID(user.ID.String())
		if err != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: partial login: %v", err)
			return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create session"})
		}
		return ctx.Response().Json(http.StatusOK, http.Json{
			"requires_2fa":  true,
			"partial_token": partialToken,
		})
	}

	accessToken, err := appfacades.Auth(ctx).LoginUsingID(user.ID.String())
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: login: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}

	rawRefresh, err := ctrl.passwords.GenerateRandomToken()
	if err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := ctrl.passwords.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := ctrl.refreshTokens.Create(ctx.Context(), rt); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
	}

	accounts, defaultAccount := ctrl.loadUserAccounts(&user)

	resp := http.Json{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"user":          user,
		"accounts":      accounts,
	}
	if defaultAccount != nil {
		resp["account_id"] = defaultAccount["id"]
		resp["account"] = defaultAccount
	}
	return ctx.Response().Json(http.StatusOK, resp)
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	payload, err := appfacades.Auth(ctx).Parse(req.PartialToken)
	if err != nil || payload == nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid or expired partial token"})
	}

	userIDStr, idErr := appfacades.Auth(ctx).ID()
	if idErr != nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid token"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid token subject"})
	}

	userPtr, findErr := ctrl.users.FindByID(ctx.Context(), userID)
	if findErr != nil || userPtr == nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "user not found"})
	}
	user := *userPtr

	verified := false
	if req.Code != "" {
		verified = ctrl.passwords.VerifyTOTP(user.TotpSecret, req.Code)
	}
	if !verified && req.RecoveryCode != "" {
		codes, codesErr := ctrl.users.FindUnusedRecoveryCodes(ctx.Context(), user.ID)
		if codesErr != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: find recovery codes: %v", codesErr)
		}
		for _, c := range codes {
			if ctrl.passwords.VerifyRecoveryCode(req.RecoveryCode, c.CodeHash) {
				if err := ctrl.users.MarkRecoveryCodeUsed(ctx.Context(), c.ID); err != nil {
					appfacades.Log().WithContext(ctx).Errorf("auth: mark recovery code used: %v", err)
				}
				verified = true
				break
			}
		}
	}

	if !verified {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	}

	accessToken, loginErr := appfacades.Auth(ctx).LoginUsingID(user.ID.String())
	if loginErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: 2fa login: %v", loginErr)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}
	rawRefresh, genErr := ctrl.passwords.GenerateRandomToken()
	if genErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", genErr)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := ctrl.passwords.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := ctrl.refreshTokens.Create(ctx.Context(), rt); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"user":          user,
	})
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
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
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid or expired refresh token"})
	}

	if err := ctrl.refreshTokens.RevokeByID(ctx.Context(), matched.ID); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: revoke refresh token: %v", err)
	}

	accessToken, loginErr := appfacades.Auth(ctx).LoginUsingID(matched.UserID.String())
	if loginErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: refresh login: %v", loginErr)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}
	rawRefresh, genErr := ctrl.passwords.GenerateRandomToken()
	if genErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", genErr)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := ctrl.passwords.HashToken(rawRefresh)
	newRT := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    matched.UserID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := ctrl.refreshTokens.Create(ctx.Context(), newRT); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
	})
}

// Logout godoc
// @Summary      Logout current user
// @Description  Revokes the current JWT and all active refresh tokens for the user
// @Tags         Auth
// @Security     BearerAuth
// @Produce      json
// @Success      204  "No content"
// @Failure      401  {object}  ErrorResponse
// @Router       /auth/logout [post]
func (ctrl *AuthController) Logout(ctx http.Context) http.Response {
	if uid, ok := requestctx.UserID(ctx); ok {
		if err := ctrl.refreshTokens.RevokeAllForUser(ctx.Context(), uid); err != nil {
			appfacades.Log().WithContext(ctx).Errorf("auth: revoke refresh tokens: %v", err)
		}
	}
	if err := appfacades.Auth(ctx).Logout(); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: logout: %v", err)
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	userPtr, findErr := ctrl.users.FindByEmail(ctx.Context(), req.Email)
	if findErr != nil || userPtr == nil {
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}
	user := *userPtr

	raw, genErr := ctrl.passwords.GenerateRandomToken()
	if genErr != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: generate reset token: %v", genErr)
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}
	hash := ctrl.passwords.HashToken(raw)
	prt := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := ctrl.passwordResets.Create(ctx.Context(), prt); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: store password reset token: %v", err)
	}

	resetLink := "https://vault.app/reset-password?token=" + raw
	if err := appfacades.Mail().To([]string{user.Email}).Send(&mails.PasswordResetMail{To: user.Email, ResetLink: resetLink}); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: send password reset mail: %v", err)
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
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
	if errResp := validateRequest(ctx, &req); errResp != nil {
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
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid or expired token"})
	}

	hash, err := ctrl.passwords.HashPassword(req.NewPassword)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if err := ctrl.users.UpdatePasswordHash(ctx.Context(), matched.UserID, hash); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: update password: %v", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	if err := ctrl.passwordResets.MarkUsed(ctx.Context(), matched.ID); err != nil {
		appfacades.Log().WithContext(ctx).Errorf("auth: mark reset token used: %v", err)
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password reset successfully"})
}

func (ctrl *AuthController) loadUserAccounts(user *models.User) ([]map[string]interface{}, map[string]interface{}) {
	memberships, err := ctrl.accounts.ListMemberships(context.Background(), user.ID)
	if err != nil {
		appfacades.Log().Errorf("auth: load memberships: %v", err)
		return nil, nil
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
	return accounts, defaultAccount
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
	PartialToken string `json:"partial_token"`
	Code         string `json:"code" example:"123456"`
	RecoveryCode string `json:"recovery_code" example:"ABCDEFGH12345678"`
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
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	User         models.User `json:"user,omitempty"`
}
