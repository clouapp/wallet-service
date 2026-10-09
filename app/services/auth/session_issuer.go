package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// refreshTokenLifetime is how long a dashboard refresh token stays usable.
const refreshTokenLifetime = 30 * 24 * time.Hour

// SessionGuard signs the session JWT of one request: the request's auth guard
// (facades.Auth(ctx)). The guard belongs to the request, so a caller passes it
// on each call instead of the service holding one.
type SessionGuard interface {
	LoginUsingID(id any) (string, error)
}

// RefreshTokenStore keeps the refresh tokens of the sessions it issues
// (sessions.RefreshTokens).
type RefreshTokenStore interface {
	Create(ctx context.Context, token *models.RefreshToken) error
}

// SessionTokens is a new dashboard session: the access JWT and the raw
// refresh token, shown once.
type SessionTokens struct {
	AccessToken  string
	RefreshToken string
}

// SessionIssuer mints dashboard sessions and replaces them after a credential
// change. It is the only place a dashboard session with a refresh token is
// created.
type SessionIssuer struct {
	passwords *Service
	refresh   RefreshTokenStore
	revoker   *SessionRevoker
}

// IssuerDeps is everything the session issuer uses. Every field is required.
type IssuerDeps struct {
	Passwords *Service
	Refresh   RefreshTokenStore
	Revoker   *SessionRevoker
}

// NewSessionIssuer builds the session issuer from IssuerDeps.
func NewSessionIssuer(deps IssuerDeps) (*SessionIssuer, error) {
	if deps.Passwords == nil || deps.Refresh == nil || deps.Revoker == nil {
		return nil, errors.New("auth: session issuer: all dependencies are required")
	}
	return &SessionIssuer{passwords: deps.Passwords, refresh: deps.Refresh, revoker: deps.Revoker}, nil
}

// Issue mints the session JWT with guard and stores the hash of a new refresh
// token. It waits out the user's revocation watermark first, so the session
// postdates it.
func (s *SessionIssuer) Issue(ctx context.Context, guard SessionGuard, userID uuid.UUID, sessionsRevokedAt *time.Time) (SessionTokens, error) {
	if userID == uuid.Nil {
		return SessionTokens{}, errors.New("issue session: user id is required")
	}
	if err := s.revoker.AwaitIssuable(sessionsRevokedAt); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: %w", err)
	}
	accessToken, err := guard.LoginUsingID(userID.String())
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: jwt: %w", err)
	}
	rawRefresh, err := s.passwords.GenerateRandomToken()
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: refresh token: %w", err)
	}
	refreshToken := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		TokenHash: s.passwords.HashToken(rawRefresh),
		ExpiresAt: time.Now().Add(refreshTokenLifetime),
	}
	if err := s.refresh.Create(ctx, refreshToken); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: store refresh token: %w", err)
	}
	return SessionTokens{AccessToken: accessToken, RefreshToken: rawRefresh}, nil
}

// Replace ends every session of the user, the caller's included, and issues
// the caller a fresh one.
func (s *SessionIssuer) Replace(ctx context.Context, guard SessionGuard, userID uuid.UUID) (SessionTokens, error) {
	watermark, err := s.revoker.RevokeAll(ctx, userID)
	if err != nil {
		return SessionTokens{}, err
	}
	return s.Issue(ctx, guard, userID, &watermark)
}
