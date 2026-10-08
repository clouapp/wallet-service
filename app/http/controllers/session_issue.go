package controllers

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
)

const refreshTokenLifetime = 30 * 24 * time.Hour

// SessionIssuer mints dashboard sessions and replaces them after a credential change.
type SessionIssuer struct {
	Passwords *authsvc.Service
	Refresh   *sessions.RefreshTokens
	Revoker   *authsvc.SessionRevoker
}

type SessionTokens struct {
	AccessToken  string
	RefreshToken string
}

// IssueSession mints the session JWT and a stored refresh token. It is the
// only place a dashboard session is created.
func (s SessionIssuer) IssueSession(ctx http.Context, userID uuid.UUID, sessionsRevokedAt *time.Time) (SessionTokens, error) {
	if s.Passwords == nil || s.Refresh == nil || s.Revoker == nil {
		return SessionTokens{}, errors.New("issue session: dependencies are required")
	}
	if userID == uuid.Nil {
		return SessionTokens{}, errors.New("issue session: user id is required")
	}
	if err := s.Revoker.AwaitIssuable(sessionsRevokedAt); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: %w", err)
	}
	accessToken, err := appfacades.Auth(ctx).LoginUsingID(userID.String())
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: jwt: %w", err)
	}
	rawRefresh, err := s.Passwords.GenerateRandomToken()
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: refresh token: %w", err)
	}
	refreshToken := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: s.Passwords.HashToken(rawRefresh),
		ExpiresAt: time.Now().Add(refreshTokenLifetime),
	}
	if err := s.Refresh.Create(ctx.Context(), refreshToken); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: store refresh token: %w", err)
	}
	return SessionTokens{AccessToken: accessToken, RefreshToken: rawRefresh}, nil
}

// ReplaceSessions ends every session of the user, the caller's included, and
// issues the caller a fresh one.
func (s SessionIssuer) ReplaceSessions(ctx http.Context, userID uuid.UUID) (SessionTokens, error) {
	if s.Revoker == nil {
		return SessionTokens{}, errors.New("replace sessions: revoker is required")
	}
	watermark, err := s.Revoker.RevokeAll(ctx.Context(), userID)
	if err != nil {
		return SessionTokens{}, err
	}
	return s.IssueSession(ctx, userID, &watermark)
}

// InactiveUserResponse answers a sign-in by a user whose status forbids a session.
func InactiveUserResponse(ctx http.Context) http.Response {
	return responses.Fail(ctx, http.StatusForbidden, responses.CodeForbidden, "user is not active")
}

// TwoFactorErrorResponse maps a second-factor failure onto the auth status codes.
func TwoFactorErrorResponse(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, authsvc.ErrChallengeInvalid):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid or expired partial token")
	case errors.Is(err, authsvc.ErrInvalidSecondFactor):
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, "invalid 2FA code")
	case errors.Is(err, authsvc.ErrSecondFactorLocked):
		return responses.Fail(ctx, http.StatusTooManyRequests, responses.CodeTooManyRequests, "too many 2FA attempts, sign in again later")
	default:
		appfacades.Log().WithContext(ctx).Errorf("auth: verify 2fa: %v", err)
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
}
