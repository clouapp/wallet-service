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

type recordingPasswordStore struct {
	hashes map[uuid.UUID]string
	err    error
}

func (s *recordingPasswordStore) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	if s.err != nil {
		return s.err
	}
	s.hashes[id] = hash
	return nil
}

type recordingResetTokens struct {
	tokens  []models.PasswordResetToken
	used    []uuid.UUID
	findErr error
	useErr  error
}

func (s *recordingResetTokens) FindValidTokens(context.Context) ([]models.PasswordResetToken, error) {
	return s.tokens, s.findErr
}

func (s *recordingResetTokens) MarkUsed(_ context.Context, id uuid.UUID) error {
	if s.useErr != nil {
		return s.useErr
	}
	s.used = append(s.used, id)
	return nil
}

type credentialsFixture struct {
	credentials *authsvc.Credentials
	passwords   *authsvc.Service
	store       *recordingPasswordStore
	resets      *recordingResetTokens
	refresh     *recordingRefreshStore
	watermarks  *fakeWatermarks
}

func newCredentials(t *testing.T) credentialsFixture {
	t.Helper()
	revoker, watermarks, _, _ := newObservedRevoker(t, time.Now())
	passwords := authsvc.NewService(newHasher())
	refresh := &recordingRefreshStore{}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{Passwords: passwords, Refresh: refresh, Revoker: revoker})
	require.NoError(t, err)
	f := credentialsFixture{
		passwords:  passwords,
		store:      &recordingPasswordStore{hashes: map[uuid.UUID]string{}},
		resets:     &recordingResetTokens{},
		refresh:    refresh,
		watermarks: watermarks,
	}
	f.credentials, err = authsvc.NewCredentials(authsvc.CredentialsDeps{
		Passwords: passwords,
		Users:     f.store,
		Resets:    f.resets,
		Sessions:  issuer,
		Revoker:   revoker,
	})
	require.NoError(t, err)
	return f
}

func (f credentialsFixture) user(t *testing.T, password string) *models.User {
	t.Helper()
	hash, err := f.passwords.HashPassword(password)
	require.NoError(t, err)
	return &models.User{ID: uuid.New(), PasswordHash: hash}
}

func TestChange_Password_StoresTheNewHashAndReplacesTheSessions(t *testing.T) {
	f := newCredentials(t)
	user := f.user(t, "old-password")

	tokens, err := f.credentials.ChangePassword(context.Background(), &recordingGuard{}, user, "old-password", "new-password")

	require.NoError(t, err)
	assert.True(t, f.passwords.CheckPassword("new-password", f.store.hashes[user.ID]))
	assert.Contains(t, f.watermarks.at, user.ID, "every session of the user is revoked")
	assert.Equal(t, "session.jwt.token", tokens.AccessToken)
	assert.Len(t, f.refresh.stored, 1)
}

func TestChange_Password_RefusesAWrongCurrentPassword(t *testing.T) {
	f := newCredentials(t)
	user := f.user(t, "old-password")

	_, err := f.credentials.ChangePassword(context.Background(), &recordingGuard{}, user, "not-it", "new-password")

	assert.ErrorIs(t, err, authsvc.ErrWrongPassword)
	assert.Empty(t, f.store.hashes)
	assert.Empty(t, f.watermarks.at)
}

func TestChange_Password_NamesTheStepThatFailed(t *testing.T) {
	outage := errors.New("store down")

	f := newCredentials(t)
	f.store.err = outage
	_, err := f.credentials.ChangePassword(context.Background(), &recordingGuard{}, f.user(t, "old-password"), "old-password", "new-password")
	assert.ErrorIs(t, err, authsvc.ErrPasswordNotSaved)
	assert.ErrorIs(t, err, outage)
	assert.Empty(t, f.watermarks.at, "sessions stay when the password did not change")

	f = newCredentials(t)
	_, err = f.credentials.ChangePassword(context.Background(), &recordingGuard{err: outage}, f.user(t, "old-password"), "old-password", "new-password")
	assert.ErrorIs(t, err, authsvc.ErrSessionsNotReplaced)
	assert.Len(t, f.store.hashes, 1, "the password is changed before the sessions")
}

func (f credentialsFixture) resetToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	raw, err := f.passwords.GenerateRandomToken()
	require.NoError(t, err)
	f.resets.tokens = append(f.resets.tokens, models.PasswordResetToken{ID: uuid.New(), UserID: userID, TokenHash: f.passwords.HashToken(raw)})
	return raw
}

func TestReset_Password_SpendsTheTokenAndEndsEverySession(t *testing.T) {
	f := newCredentials(t)
	userID := uuid.New()
	f.resetToken(t, uuid.New())
	raw := f.resetToken(t, userID)

	require.NoError(t, f.credentials.ResetPassword(context.Background(), raw, "new-password"))

	assert.True(t, f.passwords.CheckPassword("new-password", f.store.hashes[userID]))
	assert.Equal(t, []uuid.UUID{f.resets.tokens[1].ID}, f.resets.used)
	assert.Contains(t, f.watermarks.at, userID)
}

func TestReset_Password_RefusesATokenThatMatchesNoValidOne(t *testing.T) {
	f := newCredentials(t)
	f.resetToken(t, uuid.New())

	err := f.credentials.ResetPassword(context.Background(), "not-a-token", "new-password")
	assert.ErrorIs(t, err, authsvc.ErrResetTokenInvalid)
	assert.Empty(t, f.store.hashes)

	f.resets.findErr = errors.New("store down")
	err = f.credentials.ResetPassword(context.Background(), "not-a-token", "new-password")
	assert.ErrorIs(t, err, authsvc.ErrResetTokenInvalid, "a failed token read is logged and answered as no match")
}

func TestReset_Password_KeepsGoingWhenTheTokenCannotBeMarkedUsed(t *testing.T) {
	f := newCredentials(t)
	userID := uuid.New()
	raw := f.resetToken(t, userID)
	f.resets.useErr = errors.New("store down")

	require.NoError(t, f.credentials.ResetPassword(context.Background(), raw, "new-password"))
	assert.Contains(t, f.watermarks.at, userID)
}

func TestReset_Password_NamesTheStepThatFailed(t *testing.T) {
	f := newCredentials(t)
	f.store.err = errors.New("store down")
	raw := f.resetToken(t, uuid.New())

	err := f.credentials.ResetPassword(context.Background(), raw, "new-password")
	assert.ErrorIs(t, err, authsvc.ErrPasswordNotSaved)
	assert.Empty(t, f.watermarks.at)

	f = newCredentials(t)
	f.watermarks.err = errors.New("store down")
	raw = f.resetToken(t, uuid.New())
	err = f.credentials.ResetPassword(context.Background(), raw, "new-password")
	assert.ErrorIs(t, err, authsvc.ErrSessionsNotRevoked)
}
