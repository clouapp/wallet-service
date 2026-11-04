package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const sessionTestKey = "0123456789abcdef0123456789abcdef"

// jwtGuard signs a token the way the framework guard does: HS256 over a key claim.
type jwtGuard struct{}

func (jwtGuard) LoginUsingID(id any) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"key": id,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(15 * time.Minute).Unix(),
	}).SignedString([]byte(sessionTestKey))
}

func newSigningIssuer(t *testing.T) (*authsvc.SessionIssuer, *recordingRefreshStore) {
	t.Helper()
	revoker, _, _, _ := newObservedRevoker(t, time.Now())
	refresh := &recordingRefreshStore{}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{
		Passwords:  authsvc.NewService(newHasher()),
		Refresh:    refresh,
		Revoker:    revoker,
		SigningKey: sessionTestKey,
	})
	require.NoError(t, err)
	return issuer, refresh
}

func TestSession_Issuer_StampsTheSessionIDOnTheJWTAndTheRefreshToken(t *testing.T) {
	issuer, refresh := newSigningIssuer(t)
	userID := uuid.New()

	tokens, err := issuer.Issue(context.Background(), jwtGuard{}, userID, nil)

	require.NoError(t, err)
	require.Len(t, refresh.stored, 1)
	require.NotNil(t, refresh.stored[0].SessionID)
	sid, ok := authsvc.SessionIDOf(tokens.AccessToken)
	require.True(t, ok)
	assert.Equal(t, *refresh.stored[0].SessionID, sid)

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(tokens.AccessToken, claims, func(*jwt.Token) (any, error) { return []byte(sessionTestKey), nil })
	require.NoError(t, err, "the guard's key still verifies the token")
	assert.Equal(t, userID.String(), claims["key"])
}

func TestSession_Issuer_IssueInKeepsTheSessionID(t *testing.T) {
	issuer, refresh := newSigningIssuer(t)
	sid := uuid.New()

	tokens, err := issuer.IssueIn(context.Background(), jwtGuard{}, uuid.New(), nil, sid)

	require.NoError(t, err)
	got, ok := authsvc.SessionIDOf(tokens.AccessToken)
	require.True(t, ok)
	assert.Equal(t, sid, got)
	assert.Equal(t, &sid, refresh.stored[0].SessionID)
}

func TestSession_Issuer_AccessTokenOpensANewSessionWithoutARefreshToken(t *testing.T) {
	issuer, refresh := newSigningIssuer(t)

	first, err := issuer.AccessToken(jwtGuard{}, uuid.New())
	require.NoError(t, err)
	second, err := issuer.AccessToken(jwtGuard{}, uuid.New())
	require.NoError(t, err)

	firstSID, ok := authsvc.SessionIDOf(first)
	require.True(t, ok)
	secondSID, ok := authsvc.SessionIDOf(second)
	require.True(t, ok)
	assert.NotEqual(t, firstSID, secondSID)
	assert.Empty(t, refresh.stored)
}

// otherKeyGuard signs with a secret the issuer does not hold.
type otherKeyGuard struct{}

func (otherKeyGuard) LoginUsingID(id any) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"key": id}).SignedString([]byte("another-secret-another-secret-00"))
}

func TestSession_Issuer_RefusesAGuardTokenItsKeyDoesNotVerify(t *testing.T) {
	issuer, refresh := newSigningIssuer(t)

	_, err := issuer.Issue(context.Background(), otherKeyGuard{}, uuid.New(), nil)

	require.Error(t, err)
	assert.Empty(t, refresh.stored, "no refresh token is stored for a session that was not issued")
}

func TestSession_ID_OfATokenWithoutOneIsAbsent(t *testing.T) {
	token, err := jwtGuard{}.LoginUsingID(uuid.NewString())
	require.NoError(t, err)

	_, ok := authsvc.SessionIDOf(token)
	assert.False(t, ok)
	_, ok = authsvc.SessionIDOf("not-a-jwt")
	assert.False(t, ok)
}

func TestRefresh_Keeps_TheSessionIDOfTheSpentToken(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw := f.refreshToken(t, user.ID)
	sid := uuid.New()
	f.refresh.tokens[0].SessionID = &sid

	_, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)

	require.NoError(t, err)
	require.Len(t, f.stored.stored, 1)
	assert.Equal(t, &sid, f.stored.stored[0].SessionID)
}

func TestRefresh_Gives_ALegacyTokenANewSessionID(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw := f.refreshToken(t, user.ID)

	_, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)

	require.NoError(t, err)
	require.Len(t, f.stored.stored, 1)
	assert.NotNil(t, f.stored.stored[0].SessionID)
}
