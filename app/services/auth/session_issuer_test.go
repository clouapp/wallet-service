package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type recordingGuard struct {
	ids []any
	err error
}

func (g *recordingGuard) LoginUsingID(id any) (string, error) {
	g.ids = append(g.ids, id)
	if g.err != nil {
		return "", g.err
	}
	return "session.jwt.token", nil
}

type recordingRefreshStore struct {
	stored []models.RefreshToken
	err    error
}

func (s *recordingRefreshStore) Create(_ context.Context, token *models.RefreshToken) error {
	if s.err != nil {
		return s.err
	}
	s.stored = append(s.stored, *token)
	return nil
}

func newTestIssuer(t *testing.T, now time.Time) (*authsvc.SessionIssuer, *recordingRefreshStore, *fakeWatermarks, *recordedSleeps) {
	t.Helper()
	revoker, watermarks, _, sleeps := newObservedRevoker(t, now)
	refresh := &recordingRefreshStore{}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{
		Passwords: authsvc.NewService(newHasher()),
		Refresh:   refresh,
		Revoker:   revoker,
	})
	require.NoError(t, err)
	return issuer, refresh, watermarks, sleeps
}

func TestSession_Issuer_SignsTheJWTAndStoresOnlyTheRefreshTokenHash(t *testing.T) {
	issuer, refresh, _, _ := newTestIssuer(t, time.Now())
	guard := &recordingGuard{}
	userID := uuid.New()

	tokens, err := issuer.Issue(context.Background(), guard, userID, nil)

	require.NoError(t, err)
	assert.Equal(t, "session.jwt.token", tokens.AccessToken)
	assert.Equal(t, []any{userID.String()}, guard.ids)
	require.Len(t, refresh.stored, 1)
	stored := refresh.stored[0]
	assert.Equal(t, userID, stored.UserID)
	assert.NotEmpty(t, tokens.RefreshToken)
	assert.NotEqual(t, tokens.RefreshToken, stored.TokenHash, "the raw refresh token is not stored")
	assert.True(t, authsvc.NewService(newHasher()).CheckToken(tokens.RefreshToken, stored.TokenHash))
	assert.WithinDuration(t, time.Now().Add(30*24*time.Hour), stored.ExpiresAt, time.Minute)
}

func TestSession_Issuer_StoresNothingWhenTheJWTFails(t *testing.T) {
	issuer, refresh, _, _ := newTestIssuer(t, time.Now())

	_, err := issuer.Issue(context.Background(), &recordingGuard{err: errors.New("signing failed")}, uuid.New(), nil)

	require.Error(t, err)
	assert.Empty(t, refresh.stored)
}

func TestSession_Issuer_RefusesAMissingUser(t *testing.T) {
	issuer, refresh, _, _ := newTestIssuer(t, time.Now())
	guard := &recordingGuard{}

	_, err := issuer.Issue(context.Background(), guard, uuid.Nil, nil)

	require.Error(t, err)
	assert.Empty(t, guard.ids)
	assert.Empty(t, refresh.stored)
}

func TestSession_Issuer_ReplaceRevokesThenWaitsOutTheWatermark(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 5, 250_000_000, time.UTC)
	issuer, refresh, watermarks, sleeps := newTestIssuer(t, now)
	userID := uuid.New()

	tokens, err := issuer.Replace(context.Background(), &recordingGuard{}, userID)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 2, 12, 0, 6, 0, time.UTC), watermarks.at[userID])
	assert.Equal(t, []time.Duration{750 * time.Millisecond}, sleeps.waits, "the new session postdates the watermark")
	assert.Equal(t, "session.jwt.token", tokens.AccessToken)
	assert.Len(t, refresh.stored, 1)
}

func TestNew_Session_IssuerRequiresEveryDependency(t *testing.T) {
	revoker, _, _ := newTestRevoker(t, time.Now())
	for name, deps := range map[string]authsvc.IssuerDeps{
		"passwords": {Refresh: &recordingRefreshStore{}, Revoker: revoker},
		"refresh":   {Passwords: authsvc.NewService(newHasher()), Revoker: revoker},
		"revoker":   {Passwords: authsvc.NewService(newHasher()), Refresh: &recordingRefreshStore{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := authsvc.NewSessionIssuer(deps)
			require.Error(t, err)
		})
	}
}
