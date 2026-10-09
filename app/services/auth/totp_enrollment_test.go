package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// totpUsers is one user row with its TOTP columns and recovery codes. It is
// the enrollment's user store and the verifier's counter and recovery stores.
type totpUsers struct {
	user         *models.User
	counter      int64
	recovery     []models.TotpRecoveryCode
	findErr      error
	saveErr      error
	disableErr   error
	deletedCodes int
	createdCodes [][]models.TotpRecoveryCode
}

func (s *totpUsers) FindByID(context.Context, uuid.UUID) (*models.User, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	copied := *s.user
	return &copied, nil
}

func (s *totpUsers) UpdateTotpSecret(_ context.Context, _ uuid.UUID, secret string) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.user.TotpSecret = secret
	return nil
}

func (s *totpUsers) EnableTotp(context.Context, uuid.UUID) error {
	s.user.TotpEnabled = true
	return nil
}

func (s *totpUsers) DisableTotp(context.Context, uuid.UUID) error {
	if s.disableErr != nil {
		return s.disableErr
	}
	s.user.TotpEnabled = false
	s.user.TotpSecret = ""
	return nil
}

func (s *totpUsers) DeleteRecoveryCodes(context.Context, uuid.UUID) error {
	s.deletedCodes++
	s.recovery = nil
	return nil
}

func (s *totpUsers) CreateRecoveryCodes(_ context.Context, codes []models.TotpRecoveryCode) error {
	s.createdCodes = append(s.createdCodes, codes)
	s.recovery = append(s.recovery, codes...)
	return nil
}

func (s *totpUsers) SealedTotp(uuid.UUID) (string, int64, error) {
	return s.user.TotpSecret, s.counter, nil
}

func (s *totpUsers) AdvanceTotpCounter(_ uuid.UUID, counter int64) (bool, error) {
	if counter <= s.counter {
		return false, nil
	}
	s.counter = counter
	return true, nil
}

func (s *totpUsers) FindUnusedByUserID(uuid.UUID) ([]models.TotpRecoveryCode, error) {
	return s.recovery, nil
}

func (s *totpUsers) MarkUsedIfUnused(uuid.UUID) (bool, error) {
	return true, nil
}

type prefixSealer struct{ err error }

func (p prefixSealer) Seal(plaintext string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return sealedPrefix + plaintext, nil
}

type enrollmentFixture struct {
	enrollment *authsvc.TOTPEnrollment
	users      *totpUsers
	refresh    *recordingRefreshStore
	watermarks *fakeWatermarks
}

func newEnrollment(t *testing.T, user *models.User, sealer prefixSealer) enrollmentFixture {
	t.Helper()
	passwords := authsvc.NewService(newHasher())
	users := &totpUsers{user: user}
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  passwords,
		Counters: users,
		Recovery: users,
		Decrypt:  fakeDecrypt,
	})
	require.NoError(t, err)
	revoker, watermarks, _, _ := newObservedRevoker(t, time.Now())
	refresh := &recordingRefreshStore{}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{Passwords: passwords, Refresh: refresh, Revoker: revoker})
	require.NoError(t, err)
	enrollment, err := authsvc.NewTOTPEnrollment(authsvc.EnrollmentDeps{
		Passwords: passwords,
		Verifier:  verifier,
		Users:     users,
		Sealer:    sealer,
		Sessions:  issuer,
	})
	require.NoError(t, err)
	return enrollmentFixture{enrollment: enrollment, users: users, refresh: refresh, watermarks: watermarks}
}

// readProof is the proof a caller brings to Disable; reads counts how often
// Disable asked for it.
type readProof struct {
	code, recovery string
	err            error
	reads          int
}

func (p *readProof) Read() (string, string, error) {
	p.reads++
	return p.code, p.recovery, p.err
}

func newTOTPUser() *models.User {
	return &models.User{ID: uuid.New(), Email: "ada@example.com"}
}

func TestTOTP_Setup_StoresOnlyTheSealedSecret(t *testing.T) {
	f := newEnrollment(t, newTOTPUser(), prefixSealer{})

	setup, err := f.enrollment.Setup(context.Background(), f.users.user)

	require.NoError(t, err)
	assert.NotEmpty(t, setup.Secret)
	assert.Contains(t, setup.QRURL, "otpauth://totp/")
	assert.Equal(t, sealedPrefix+setup.Secret, f.users.user.TotpSecret)
	assert.False(t, f.users.user.TotpEnabled, "setup does not turn 2FA on")
}

func TestTOTP_Setup_RefusesAUserWhoHasIt(t *testing.T) {
	user := newTOTPUser()
	user.TotpEnabled = true
	f := newEnrollment(t, user, prefixSealer{})

	_, err := f.enrollment.Setup(context.Background(), user)

	assert.ErrorIs(t, err, authsvc.ErrTOTPAlreadyEnabled)
	assert.Empty(t, f.users.user.TotpSecret)
}

func TestTOTP_Setup_NamesTheStepThatFailed(t *testing.T) {
	f := newEnrollment(t, newTOTPUser(), prefixSealer{err: errors.New("crypt down")})
	_, err := f.enrollment.Setup(context.Background(), f.users.user)
	assert.ErrorIs(t, err, authsvc.ErrTOTPNotSealed)
	assert.Empty(t, f.users.user.TotpSecret, "an unsealed secret is never stored")

	f = newEnrollment(t, newTOTPUser(), prefixSealer{})
	f.users.saveErr = errors.New("store down")
	_, err = f.enrollment.Setup(context.Background(), f.users.user)
	assert.ErrorIs(t, err, authsvc.ErrTOTPNotSaved)
}

func TestTOTP_Confirm_TurnsItOnWithFreshRecoveryCodes(t *testing.T) {
	f := newEnrollment(t, newTOTPUser(), prefixSealer{})
	setup, err := f.enrollment.Setup(context.Background(), f.users.user)
	require.NoError(t, err)
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	require.NoError(t, err)
	f.users.recovery = []models.TotpRecoveryCode{{ID: uuid.New()}}

	confirmed, err := f.enrollment.Confirm(context.Background(), f.users.user, code)

	require.NoError(t, err)
	assert.True(t, f.users.user.TotpEnabled)
	assert.True(t, confirmed.User.TotpEnabled)
	assert.Empty(t, confirmed.User.TotpSecret, "the returned user carries no secret")
	assert.Len(t, confirmed.RecoveryCodes, 10)
	assert.Equal(t, 1, f.users.deletedCodes, "the old recovery codes are replaced")
	require.Len(t, f.users.createdCodes, 1)
	require.Len(t, f.users.createdCodes[0], 10)
	assert.NotEqual(t, confirmed.RecoveryCodes[0], f.users.createdCodes[0][0].CodeHash, "only hashes are stored")
	assert.Positive(t, f.users.counter, "the confirming code cannot be replayed at login")
}

func TestTOTP_Confirm_RefusesWhatCannotBeConfirmed(t *testing.T) {
	f := newEnrollment(t, newTOTPUser(), prefixSealer{})
	_, err := f.enrollment.Confirm(context.Background(), f.users.user, "123456")
	assert.ErrorIs(t, err, authsvc.ErrTOTPNotStarted, "no secret was set up")

	_, err = f.enrollment.Setup(context.Background(), f.users.user)
	require.NoError(t, err)
	_, err = f.enrollment.Confirm(context.Background(), f.users.user, "000000")
	assert.ErrorIs(t, err, authsvc.ErrTOTPCodeInvalid)
	assert.False(t, f.users.user.TotpEnabled)

	f.users.user.TotpSecret = "not-sealed"
	_, err = f.enrollment.Confirm(context.Background(), f.users.user, "000000")
	assert.ErrorIs(t, err, authsvc.ErrTOTPNotOpened)
}

func enabledUser(t *testing.T) (*models.User, string) {
	t.Helper()
	secret, _, err := authsvc.NewService(newHasher()).GenerateTOTP("ada@example.com")
	require.NoError(t, err)
	return &models.User{ID: uuid.New(), Email: "ada@example.com", TotpEnabled: true, TotpSecret: sealedPrefix + secret}, secret
}

func TestTOTP_Disable_NeedsALiveSecondFactor(t *testing.T) {
	user, _ := enabledUser(t)
	f := newEnrollment(t, user, prefixSealer{})

	_, err := f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, &readProof{code: "  "})
	assert.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
	_, err = f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, &readProof{code: "000000"})
	assert.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
	unreadable := errors.New("body does not bind")
	_, err = f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, &readProof{err: unreadable})
	assert.ErrorIs(t, err, unreadable, "a proof that cannot be read is returned as it is")
	assert.True(t, f.users.user.TotpEnabled)
	assert.Empty(t, f.watermarks.at)
}

func TestTOTP_Disable_TurnsItOffAndReplacesTheSessions(t *testing.T) {
	user, secret := enabledUser(t)
	f := newEnrollment(t, user, prefixSealer{})
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	disabled, err := f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, &readProof{code: " " + code + " "})

	require.NoError(t, err)
	assert.False(t, f.users.user.TotpEnabled)
	assert.Equal(t, 1, f.users.deletedCodes)
	assert.Contains(t, f.watermarks.at, user.ID)
	assert.Equal(t, "session.jwt.token", disabled.Tokens.AccessToken)
	assert.False(t, disabled.User.TotpEnabled)
	assert.Empty(t, disabled.User.TotpSecret)
}

func TestTOTP_Disable_AsksNoProofOfAUserWithoutIt(t *testing.T) {
	user := newTOTPUser()
	f := newEnrollment(t, user, prefixSealer{})
	proof := &readProof{err: errors.New("body does not bind")}

	_, err := f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, proof)

	require.NoError(t, err)
	assert.Zero(t, proof.reads, "the proof of a user without 2FA is not read")
	assert.Contains(t, f.watermarks.at, user.ID)
}

func TestTOTP_Disable_NamesTheStepThatFailed(t *testing.T) {
	user := newTOTPUser()
	f := newEnrollment(t, user, prefixSealer{})
	f.users.findErr = errors.New("store down")
	_, err := f.enrollment.Disable(context.Background(), &recordingGuard{}, user.ID, &readProof{})
	assert.ErrorIs(t, err, authsvc.ErrUserNotFound)

	f = newEnrollment(t, newTOTPUser(), prefixSealer{})
	f.users.disableErr = errors.New("store down")
	_, err = f.enrollment.Disable(context.Background(), &recordingGuard{}, f.users.user.ID, &readProof{})
	assert.ErrorIs(t, err, authsvc.ErrTOTPNotDisabled)
	assert.Empty(t, f.watermarks.at)

	f = newEnrollment(t, newTOTPUser(), prefixSealer{})
	_, err = f.enrollment.Disable(context.Background(), &recordingGuard{err: errors.New("signing failed")}, f.users.user.ID, &readProof{})
	assert.ErrorIs(t, err, authsvc.ErrSessionsNotReplaced)
	assert.False(t, f.users.user.TotpEnabled, "2FA is off before the sessions are replaced")
}
