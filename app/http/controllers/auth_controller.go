package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
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
// @Param        request  body      RegisterRequest  true  "Registration payload"
// @Success      201      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      409      {object}  ErrorResponse
// @Router       /auth/register [post]
func Register(ctx http.Context) http.Response {
	var req RegisterRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Email == "" || req.Password == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "email and password are required"})
	}
	if req.OrganizationName == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "organization_name is required"})
	}

	existing, _ := container.Get().UserRepo.FindByEmail(req.Email)
	if existing != nil {
		return ctx.Response().Json(http.StatusConflict, http.Json{"error": "email already in use"})
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
	_ = container.Get().AccountUserRepo.Create(prodMembership)
	_ = container.Get().AccountUserRepo.Create(testMembership)

	user.DefaultAccountID = &prodAccountID
	_ = container.Get().UserRepo.UpdateDefaultAccountID(user.ID, &prodAccountID)

	// Send welcome email (best-effort)
	_ = facades.Mail().To([]string{user.Email}).Send(&mails.WelcomeMail{To: user.Email, FullName: user.FullName})

	accessToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())

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
// @Param        request  body      LoginRequest  true  "Login credentials"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/login [post]
func Login(ctx http.Context) http.Response {
	var req LoginRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	userPtr, _ := container.Get().UserRepo.FindByEmail(req.Email)
	if userPtr == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}
	user := *userPtr

	if !authService.CheckPassword(req.Password, user.PasswordHash) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}

	if user.TotpEnabled {
		// Issue a short-lived partial token — the client must call /auth/2fa/verify
		partialToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())
		return ctx.Response().Json(http.StatusOK, http.Json{
			"requires_2fa":  true,
			"partial_token": partialToken,
		})
	}

	accessToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())

	// Issue refresh token
	rawRefresh, _ := authService.GenerateRandomToken()
	refreshHash := authService.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	_ = container.Get().RefreshTokenRepo.Create(rt)

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
// @Param        request  body      TwoFactorRequest  true  "2FA verification payload"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/2fa/verify [post]
func VerifyTwoFactor(ctx http.Context) http.Response {
	var req TwoFactorRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	// The partial token issued during Login must be provided
	if req.PartialToken == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "partial_token is required"})
	}

	payload, err := facades.Auth(ctx).Parse(req.PartialToken)
	if err != nil || payload == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired partial token"})
	}

	userIDStr, _ := facades.Auth(ctx).ID()
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid token subject"})
	}

	userPtr, _ := container.Get().UserRepo.FindByID(userID)
	if userPtr == nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
	}
	user := *userPtr

	verified := false
	if req.Code != "" {
		verified = authService.VerifyTOTP(user.TotpSecret, req.Code)
	}
	if !verified && req.RecoveryCode != "" {
		codes, _ := container.Get().TotpRecoveryCodeRepo.FindUnusedByUserID(user.ID)
		for _, c := range codes {
			if authService.VerifyRecoveryCode(req.RecoveryCode, c.CodeHash) {
				_ = container.Get().TotpRecoveryCodeRepo.MarkUsed(c.ID)
				verified = true
				break
			}
		}
	}

	if !verified {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	}

	accessToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())
	rawRefresh, _ := authService.GenerateRandomToken()
	refreshHash := authService.HashToken(rawRefresh)
	rt := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	_ = container.Get().RefreshTokenRepo.Create(rt)

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
// @Param        request  body      RefreshTokenRequest  true  "Refresh token"
// @Success      200      {object}  AuthResponse
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/refresh [post]
func RefreshToken(ctx http.Context) http.Response {
	var req RefreshTokenRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.RefreshToken == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "refresh_token is required"})
	}

	tokens, _ := container.Get().RefreshTokenRepo.FindValidTokens()

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

	_ = container.Get().RefreshTokenRepo.RevokeByID(matched.ID)

	accessToken, _ := facades.Auth(ctx).LoginUsingID(matched.UserID.String())
	rawRefresh, _ := authService.GenerateRandomToken()
	refreshHash := authService.HashToken(rawRefresh)
	newRT := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    matched.UserID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	_ = container.Get().RefreshTokenRepo.Create(newRT)

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
		_ = container.Get().RefreshTokenRepo.RevokeAllForUser(uid)
	}
	_ = facades.Auth(ctx).Logout()
	return ctx.Response().NoContent()
}

// ForgotPassword godoc
// @Summary      Request password reset email
// @Description  Sends a password reset link to the user's email if the address is registered
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      ForgotPasswordRequest  true  "Email address"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  ErrorResponse
// @Router       /auth/forgot-password [post]
func ForgotPassword(ctx http.Context) http.Response {
	var req ForgotPasswordRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	// Always return 200 to avoid user enumeration
	userPtr, _ := container.Get().UserRepo.FindByEmail(req.Email)
	if userPtr == nil {
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}
	user := *userPtr

	raw, _ := authService.GenerateRandomToken()
	hash := authService.HashToken(raw)
	prt := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	_ = container.Get().PasswordResetTokenRepo.Create(prt)

	resetLink := "https://vault.app/reset-password?token=" + raw
	_ = facades.Mail().To([]string{user.Email}).Send(&mails.PasswordResetMail{To: user.Email, ResetLink: resetLink})

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
}

// ResetPassword godoc
// @Summary      Reset password using token
// @Description  Validates the reset token and updates the user's password
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        request  body      ResetPasswordRequest  true  "Token and new password"
// @Success      200      {object}  map[string]string
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Router       /auth/reset-password [post]
func ResetPassword(ctx http.Context) http.Response {
	var req ResetPasswordRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.Token == "" || req.NewPassword == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "token and new_password are required"})
	}

	tokens, _ := container.Get().PasswordResetTokenRepo.FindValidTokens()

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

	_ = container.Get().UserRepo.UpdatePasswordHash(matched.UserID, hash)

	_ = container.Get().PasswordResetTokenRepo.MarkUsed(matched.ID)

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password reset successfully"})
}

// loadUserAccounts fetches the user's account memberships and builds a
// serialisable list. It also picks the default account (user.DefaultAccountID
// or the first one found).
func loadUserAccounts(user *models.User) ([]map[string]interface{}, map[string]interface{}) {
	memberships, _ := container.Get().AccountUserRepo.FindByUserID(user.ID)

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

// ---- Request/Response types ----

type RegisterRequest struct {
	Email            string `json:"email" example:"user@example.com"`
	Password         string `json:"password" example:"s3cr3t"`
	FullName         string `json:"full_name" example:"Alice Smith"`
	OrganizationName string `json:"organization_name" example:"Acme Corp"`
}

type LoginRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"s3cr3t"`
}

type TwoFactorRequest struct {
	PartialToken string `json:"partial_token"`
	Code         string `json:"code" example:"123456"`
	RecoveryCode string `json:"recovery_code" example:"ABCDEFGH12345678"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email" example:"user@example.com"`
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password" example:"newS3cr3t"`
}

type AuthResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	User         models.User `json:"user,omitempty"`
}
