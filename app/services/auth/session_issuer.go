package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	signKey   []byte
}

// SessionIDClaim is the JWT claim that carries the id of the session
// (refresh_tokens.session_id). A token issued before it existed has none.
const SessionIDClaim = "sid"

// IssuerDeps is everything the session issuer uses. Every field is required.
type IssuerDeps struct {
	Passwords *Service
	Refresh   RefreshTokenStore
	Revoker   *SessionRevoker
	// SigningKey is the jwt.secret the guard signs with. When set, the access
	// token is verified with it and re-signed with the session id as the "sid"
	// claim. The composition root refuses an empty one; only test fakes whose
	// guards do not sign leave it empty.
	SigningKey string
}

// NewSessionIssuer builds the session issuer from IssuerDeps.
func NewSessionIssuer(deps IssuerDeps) (*SessionIssuer, error) {
	if deps.Passwords == nil || deps.Refresh == nil || deps.Revoker == nil {
		return nil, errors.New("auth: session issuer: all dependencies are required")
	}
	return &SessionIssuer{passwords: deps.Passwords, refresh: deps.Refresh, revoker: deps.Revoker, signKey: []byte(deps.SigningKey)}, nil
}

// Issue mints the session JWT with guard and stores the hash of a new refresh
// token, under a new session id. It waits out the user's revocation watermark
// first, so the session postdates it.
func (s *SessionIssuer) Issue(ctx context.Context, guard SessionGuard, userID uuid.UUID, sessionsRevokedAt *time.Time) (SessionTokens, error) {
	return s.IssueIn(ctx, guard, userID, sessionsRevokedAt, uuid.New())
}

// IssueIn is Issue for an existing session: a refresh keeps the session id of
// the token it spends.
func (s *SessionIssuer) IssueIn(ctx context.Context, guard SessionGuard, userID uuid.UUID, sessionsRevokedAt *time.Time, sessionID uuid.UUID) (SessionTokens, error) {
	if userID == uuid.Nil {
		return SessionTokens{}, errors.New("issue session: user id is required")
	}
	if err := s.revoker.AwaitIssuable(sessionsRevokedAt); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: %w", err)
	}
	accessToken, err := s.accessToken(guard, userID, sessionID)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: %w", err)
	}
	rawRefresh, err := s.passwords.GenerateRandomToken()
	if err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: refresh token: %w", err)
	}
	refreshToken := &models.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		SessionID: &sessionID,
		TokenHash: s.passwords.HashToken(rawRefresh),
		ExpiresAt: time.Now().Add(refreshTokenLifetime),
	}
	if err := s.refresh.Create(ctx, refreshToken); err != nil {
		return SessionTokens{}, fmt.Errorf("issue session: store refresh token: %w", err)
	}
	return SessionTokens{AccessToken: accessToken, RefreshToken: rawRefresh}, nil
}

// AccessToken mints a session JWT under a new session id without a refresh
// token: the session a registration opens.
func (s *SessionIssuer) AccessToken(guard SessionGuard, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", errors.New("access token: user id is required")
	}
	return s.accessToken(guard, userID, uuid.New())
}

// accessToken signs the guard's JWT for userID and stamps it with sessionID.
func (s *SessionIssuer) accessToken(guard SessionGuard, userID, sessionID uuid.UUID) (string, error) {
	token, err := guard.LoginUsingID(userID.String())
	if err != nil {
		return "", fmt.Errorf("jwt: %w", err)
	}
	if len(s.signKey) == 0 {
		return token, nil
	}
	if token, err = s.withSessionID(token, sessionID); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	return token, nil
}

// withSessionID adds the sid claim to the guard's token and signs it again
// with the same method and key; every other claim stays as the guard wrote it.
// A token the key does not verify (the guard signs with another secret or
// method) fails the issue instead of handing out a token the guard refuses.
func (s *SessionIssuer) withSessionID(token string, sessionID uuid.UUID) (string, error) {
	claims := jwt.MapClaims{}
	parsed, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return s.signKey, nil
	})
	if err != nil {
		return "", fmt.Errorf("the signing key does not verify the guard's token: %w", err)
	}
	claims[SessionIDClaim] = sessionID.String()
	return jwt.NewWithClaims(parsed.Method, claims).SignedString(s.signKey)
}

// SessionIDOf reads the sid claim of a token the guard has already verified.
// A token without one (issued before sessions had ids) is uuid.Nil, false.
func SessionIDOf(token string) (uuid.UUID, bool) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return uuid.Nil, false
	}
	raw, _ := claims[SessionIDClaim].(string)
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
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
