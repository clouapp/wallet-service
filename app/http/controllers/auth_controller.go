package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	mails "github.com/macrowallets/waas/app/mails"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

var authService = authsvc.NewService()

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
func Register(ctx http.Context) http.Response {
	var req requests.RegisterRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	hash, err := authService.HashPassword(req.Password)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	user := &models.User{
		ID:           uuid.New(),
		Email:        req.Email,
		PasswordHash: hash,
		FullName:     req.FullName,
		Status:       "active",
	}
	if err := container.Get().UserRepo.Create(user); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create user"})
	}

	prodAccountID := uuid.New()
	testAccountID := uuid.New()

	prodAccount := &models.Account{
		ID:              prodAccountID,
		Name:            req.OrganizationName,
		Status:          "active",
		Environment:     models.EnvironmentProd,
		LinkedAccountID: &testAccountID,
	}
	testAccount := &models.Account{
		ID:              testAccountID,
		Name:            req.OrganizationName + " [test]",
		Status:          "active",
		Environment:     models.EnvironmentTest,
		LinkedAccountID: &prodAccountID,
	}
	if err := container.Get().AccountRepo.Create(prodAccount); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create production account"})
	}
	if err := container.Get().AccountRepo.Create(testAccount); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create test account"})
	}

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
	if err := container.Get().AccountUserRepo.Create(prodMembership); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: create prod membership: %v", err)
	}
	if err := container.Get().AccountUserRepo.Create(testMembership); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: create test membership: %v", err)
	}

	user.DefaultAccountID = &prodAccountID
	if err := container.Get().UserRepo.UpdateDefaultAccountID(user.ID, &prodAccountID); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: set default account: %v", err)
	}

	if err := facades.Mail().To([]string{user.Email}).Send(&mails.WelcomeMail{To: user.Email, FullName: user.FullName}); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: send welcome mail: %v", err)
	}

	accessToken, err := facades.Auth(ctx).LoginUsingID(user.ID.String())
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: login after register: %v", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}

	accounts, defaultAccount := loadUserAccounts(user)

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
func Login(ctx http.Context) http.Response {
	var req requests.LoginRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	userPtr, err := container.Get().UserRepo.FindByEmail(req.Email)
	if err != nil || userPtr == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}
	user := *userPtr

	if !authService.CheckPassword(req.Password, user.PasswordHash) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}

	if user.TotpEnabled {
		partialToken, err := facades.Auth(ctx).LoginUsingID(user.ID.String())
		if err != nil {
			facades.Log().WithContext(ctx).Errorf("auth: partial login: %v", err)
			return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create session"})
		}
		return ctx.Response().Json(http.StatusOK, http.Json{
			"requires_2fa":  true,
			"partial_token": partialToken,
		})
	}

	accessToken, err := facades.Auth(ctx).LoginUsingID(user.ID.String())
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: login: %v", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}

	rawRefresh, err := authService.GenerateRandomToken()
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := authService.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := container.Get().RefreshTokenRepo.Create(rt); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
	}

	accounts, defaultAccount := loadUserAccounts(&user)

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
func VerifyTwoFactor(ctx http.Context) http.Response {
	var req requests.VerifyTwoFactorRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	payload, err := facades.Auth(ctx).Parse(req.PartialToken)
	if err != nil || payload == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired partial token"})
	}

	userIDStr, idErr := facades.Auth(ctx).ID()
	if idErr != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token"})
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token subject"})
	}

	userPtr, findErr := container.Get().UserRepo.FindByID(userID)
	if findErr != nil || userPtr == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
	}
	user := *userPtr

	verified := false
	if req.Code != "" {
		verified = authService.VerifyTOTP(user.TotpSecret, req.Code)
	}
	if !verified && req.RecoveryCode != "" {
		codes, codesErr := container.Get().TotpRecoveryCodeRepo.FindUnusedByUserID(user.ID)
		if codesErr != nil {
			facades.Log().WithContext(ctx).Errorf("auth: find recovery codes: %v", codesErr)
		}
		for _, c := range codes {
			if authService.VerifyRecoveryCode(req.RecoveryCode, c.CodeHash) {
				if err := container.Get().TotpRecoveryCodeRepo.MarkUsed(c.ID); err != nil {
					facades.Log().WithContext(ctx).Errorf("auth: mark recovery code used: %v", err)
				}
				verified = true
				break
			}
		}
	}

	if !verified {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	}

	accessToken, loginErr := facades.Auth(ctx).LoginUsingID(user.ID.String())
	if loginErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: 2fa login: %v", loginErr)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}
	rawRefresh, genErr := authService.GenerateRandomToken()
	if genErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", genErr)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := authService.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := container.Get().RefreshTokenRepo.Create(rt); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
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
func RefreshToken(ctx http.Context) http.Response {
	var req requests.RefreshTokenRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	tokens, tokErr := container.Get().RefreshTokenRepo.FindValidTokens()
	if tokErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: find refresh tokens: %v", tokErr)
	}

	var matched *models.RefreshToken
	for i := range tokens {
		if authService.CheckToken(req.RefreshToken, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired refresh token"})
	}

	if err := container.Get().RefreshTokenRepo.RevokeByID(matched.ID); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: revoke refresh token: %v", err)
	}

	accessToken, loginErr := facades.Auth(ctx).LoginUsingID(matched.UserID.String())
	if loginErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: refresh login: %v", loginErr)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create session"})
	}
	rawRefresh, genErr := authService.GenerateRandomToken()
	if genErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: generate refresh token: %v", genErr)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	refreshHash := authService.HashToken(rawRefresh)
	newRT := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    matched.UserID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	if err := container.Get().RefreshTokenRepo.Create(newRT); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: store refresh token: %v", err)
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
func Logout(ctx http.Context) http.Response {
	userID := ctx.Value("user_id")
	if uid, ok := userID.(uuid.UUID); ok {
		if err := container.Get().RefreshTokenRepo.RevokeAllForUser(uid); err != nil {
			facades.Log().WithContext(ctx).Errorf("auth: revoke refresh tokens: %v", err)
		}
	}
	if err := facades.Auth(ctx).Logout(); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: logout: %v", err)
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
func ForgotPassword(ctx http.Context) http.Response {
	var req requests.ForgotPasswordRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	userPtr, findErr := container.Get().UserRepo.FindByEmail(req.Email)
	if findErr != nil || userPtr == nil {
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}
	user := *userPtr

	raw, genErr := authService.GenerateRandomToken()
	if genErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: generate reset token: %v", genErr)
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}
	hash := authService.HashToken(raw)
	prt := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := container.Get().PasswordResetTokenRepo.Create(prt); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: store password reset token: %v", err)
	}

	resetLink := "https://vault.app/reset-password?token=" + raw
	if err := facades.Mail().To([]string{user.Email}).Send(&mails.PasswordResetMail{To: user.Email, ResetLink: resetLink}); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: send password reset mail: %v", err)
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
func ResetPassword(ctx http.Context) http.Response {
	var req requests.ResetPasswordRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	tokens, tokErr := container.Get().PasswordResetTokenRepo.FindValidTokens()
	if tokErr != nil {
		facades.Log().WithContext(ctx).Errorf("auth: find reset tokens: %v", tokErr)
	}

	var matched *models.PasswordResetToken
	for i := range tokens {
		if authService.CheckToken(req.Token, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired token"})
	}

	hash, err := authService.HashPassword(req.NewPassword)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to hash password"})
	}

	if err := container.Get().UserRepo.UpdatePasswordHash(matched.UserID, hash); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: update password: %v", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to update password"})
	}

	if err := container.Get().PasswordResetTokenRepo.MarkUsed(matched.ID); err != nil {
		facades.Log().WithContext(ctx).Errorf("auth: mark reset token used: %v", err)
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password reset successfully"})
}

func loadUserAccounts(user *models.User) ([]map[string]interface{}, map[string]interface{}) {
	memberships, err := container.Get().AccountUserRepo.FindByUserID(user.ID)
	if err != nil {
		facades.Log().Errorf("auth: load memberships: %v", err)
		return nil, nil
	}

	var accounts []map[string]interface{}
	var defaultAccount map[string]interface{}

	for _, m := range memberships {
		acct, err := container.Get().AccountRepo.FindByID(m.AccountID)
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
