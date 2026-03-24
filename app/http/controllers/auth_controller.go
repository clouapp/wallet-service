package controllers

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	mails "github.com/macromarkets/vault/app/mails"
	"github.com/macromarkets/vault/app/models"
	authsvc "github.com/macromarkets/vault/app/services/auth"
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

	// Check for existing user
	var existing models.User
	if err := facades.Orm().Query().Where("email = ?", req.Email).First(&existing); err == nil {
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
	if err := facades.Orm().Query().Create(user); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create user"})
	}

	// Send welcome email (best-effort)
	_ = facades.Mail().To([]string{user.Email}).Send(&mails.WelcomeMail{To: user.Email, FullName: user.FullName})

	accessToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())
	return ctx.Response().Json(http.StatusCreated, http.Json{
		"access_token": accessToken,
		"user":         user,
	})
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

	var user models.User
	if err := facades.Orm().Query().Where("email = ?", req.Email).First(&user); err != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}

	if !authService.CheckPassword(req.Password, user.PasswordHash) {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid credentials"})
	}

	if user.TotpEnabled {
		// Issue a short-lived partial token — the client must call /auth/2fa/verify
		partialToken, _ := facades.Auth(ctx).LoginUsingID(user.ID.String())
		return ctx.Response().Json(http.StatusOK, http.Json{
			"requires_2fa": true,
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
	_ = facades.Orm().Query().Create(rt)

	return ctx.Response().Json(http.StatusOK, http.Json{
		"access_token":  accessToken,
		"refresh_token": rawRefresh,
		"user":          user,
	})
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

	var user models.User
	if err := facades.Orm().Query().Where("id = ?", userID).First(&user); err != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "user not found"})
	}

	verified := false
	if req.Code != "" {
		verified = authService.VerifyTOTP(user.TotpSecret, req.Code)
	}
	if !verified && req.RecoveryCode != "" {
		// Find an unused recovery code that matches
		var codes []models.TotpRecoveryCode
		_ = facades.Orm().Query().
			Where("user_id = ? AND used_at IS NULL", user.ID).
			Find(&codes)
		for _, c := range codes {
			if authService.VerifyRecoveryCode(req.RecoveryCode, c.CodeHash) {
				now := time.Now()
				_, _ = facades.Orm().Query().Model(&c).Where("id = ?", c.ID).Update("used_at", now)
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
	_ = facades.Orm().Query().Create(rt)

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

	// Find a matching, non-expired, non-revoked refresh token
	var tokens []models.RefreshToken
	_ = facades.Orm().Query().
		Where("expires_at > ? AND revoked_at IS NULL", time.Now()).
		Find(&tokens)

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

	// Revoke old token
	now := time.Now()
	_, _ = facades.Orm().Query().Model(matched).Where("id = ?", matched.ID).Update("revoked_at", now)

	accessToken, _ := facades.Auth(ctx).LoginUsingID(matched.UserID.String())
	rawRefresh, _ := authService.GenerateRandomToken()
	refreshHash := authService.HashToken(rawRefresh)
	newRT := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    matched.UserID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	_ = facades.Orm().Query().Create(newRT)

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
		now := time.Now()
		_, _ = facades.Orm().Query().
			Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", uid).
			Update("revoked_at", now)
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
	var user models.User
	if err := facades.Orm().Query().Where("email = ?", req.Email).First(&user); err != nil {
		return ctx.Response().Json(http.StatusOK, http.Json{"message": "if that address is registered, you will receive a reset link"})
	}

	raw, _ := authService.GenerateRandomToken()
	hash := authService.HashToken(raw)
	prt := &models.PasswordResetToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	_ = facades.Orm().Query().Create(prt)

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

	var tokens []models.PasswordResetToken
	_ = facades.Orm().Query().
		Where("expires_at > ? AND used_at IS NULL", time.Now()).
		Find(&tokens)

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

	_, _ = facades.Orm().Query().Model(&models.User{}).
		Where("id = ?", matched.UserID).
		Update("password_hash", hash)

	now := time.Now()
	_, _ = facades.Orm().Query().Model(matched).Where("id = ?", matched.ID).Update("used_at", now)

	return ctx.Response().Json(http.StatusOK, http.Json{"message": "password reset successfully"})
}

// ---- Request/Response types ----

type RegisterRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"s3cr3t"`
	FullName string `json:"full_name" example:"Alice Smith"`
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
