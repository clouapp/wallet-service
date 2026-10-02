package controllers

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const refreshTokenLifetime = 30 * 24 * time.Hour

type sessionTokens struct {
	AccessToken  string
	RefreshToken string
}

// issueSession mints the session JWT and a stored refresh token. It is the
// only place a dashboard session is created, so every path that grants one
// (password without TOTP, completed 2FA, refresh) issues the same pair.
func issueSession(ctx http.Context, userID uuid.UUID, sessionsRevokedAt *time.Time) (sessionTokens, error) {
	if userID == uuid.Nil {
		return sessionTokens{}, errors.New("issue session: user id is required")
	}
	if err := container.Get().SessionRevoker.AwaitIssuable(sessionsRevokedAt); err != nil {
		return sessionTokens{}, fmt.Errorf("issue session: %w", err)
	}
	accessToken, err := facades.Auth(ctx).LoginUsingID(userID.String())
	if err != nil {
		return sessionTokens{}, fmt.Errorf("issue session: jwt: %w", err)
	}
	rawRefresh, err := authService.GenerateRandomToken()
	if err != nil {
		return sessionTokens{}, fmt.Errorf("issue session: refresh token: %w", err)
	}
	refreshToken := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: authService.HashToken(rawRefresh),
		ExpiresAt: time.Now().Add(refreshTokenLifetime),
	}
	if err := container.Get().RefreshTokenRepo.Create(refreshToken); err != nil {
		return sessionTokens{}, fmt.Errorf("issue session: store refresh token: %w", err)
	}
	return sessionTokens{AccessToken: accessToken, RefreshToken: rawRefresh}, nil
}

// replaceSessions ends every session of the user, the caller's included, and
// issues the caller a fresh one. It is for credential changes made by a user
// who just proved who they are (password change, disabling TOTP).
func replaceSessions(ctx http.Context, userID uuid.UUID) (sessionTokens, error) {
	watermark, err := container.Get().SessionRevoker.RevokeAll(userID)
	if err != nil {
		return sessionTokens{}, err
	}
	return issueSession(ctx, userID, &watermark)
}

// signedInResponse is the body of every response that completes a sign-in.
func signedInResponse(user *models.User, tokens sessionTokens) http.Json {
	accounts, defaultAccount := loadUserAccounts(user)
	resp := http.Json{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"user":          user,
		"accounts":      accounts,
	}
	if defaultAccount != nil {
		resp["account_id"] = defaultAccount["id"]
		resp["account"] = defaultAccount
	}
	return resp
}

func twoFactorErrorResponse(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, authsvc.ErrChallengeInvalid):
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid or expired partial token"})
	case errors.Is(err, authsvc.ErrInvalidSecondFactor):
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid 2FA code"})
	case errors.Is(err, authsvc.ErrSecondFactorLocked):
		return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": "too many 2FA attempts, sign in again later"})
	default:
		facades.Log().WithContext(ctx).Errorf("auth: verify 2fa: %v", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
}
