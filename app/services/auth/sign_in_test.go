package auth_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

type signInUsers struct {
	byEmail map[string]*models.User
	byID    map[uuid.UUID]*models.User
	err     error
	idErr   error
}

func (u *signInUsers) FindByEmail(_ context.Context, email string) (*models.User, error) {
	if u.err != nil {
		return nil, u.err
	}
	user, ok := u.byEmail[email]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	copied := *user
	return &copied, nil
}

func (u *signInUsers) FindByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	if u.idErr != nil {
		return nil, u.idErr
	}
	user, ok := u.byID[id]
	if !ok {
		return nil, nil
	}
	copied := *user
	return &copied, nil
}

type signInAccountsFake struct {
	accounts   account.SignInAccounts
	err        error
	onboardErr error
	onboarded  []account.OnboardInput
	reads      int
}

func (a *signInAccountsFake) Onboard(_ context.Context, in account.OnboardInput, welcome account.WelcomeDispatch) (*models.User, error) {
	if a.onboardErr != nil {
		return nil, a.onboardErr
	}
	a.onboarded = append(a.onboarded, in)
	user := &models.User{ID: uuid.New(), Email: in.Email, FullName: in.FullName, PasswordHash: in.PasswordHash, Status: models.StatusActive}
	if welcome != nil {
		if err := welcome(user.ID); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func (a *signInAccountsFake) SignInAccounts(context.Context, uuid.UUID, *uuid.UUID) (account.SignInAccounts, error) {
	a.reads++
	return a.accounts, a.err
}

type recordingWelcome struct {
	sent []uuid.UUID
	err  error
}

func (w *recordingWelcome) SendWelcome(_ context.Context, userID uuid.UUID) error {
	w.sent = append(w.sent, userID)
	return w.err
}

type rotatingRefresh struct {
	tokens    []models.RefreshToken
	findErr   error
	rotated   bool
	rotateErr error
	revoked   []uuid.UUID
}

func (r *rotatingRefresh) FindValidTokens(context.Context) ([]models.RefreshToken, error) {
	return r.tokens, r.findErr
}

func (r *rotatingRefresh) RevokeIfActive(_ context.Context, id uuid.UUID) (bool, error) {
	if r.rotateErr != nil {
		return false, r.rotateErr
	}
	r.revoked = append(r.revoked, id)
	return r.rotated, nil
}

type signInFixture struct {
	signIn     *authsvc.SignIn
	passwords  *authsvc.Service
	users      *signInUsers
	accounts   *signInAccountsFake
	welcome    *recordingWelcome
	refresh    *rotatingRefresh
	stored     *recordingRefreshStore
	watermarks *fakeWatermarks
	twoFactor  *twoFactorFixture
}

func newSignIn(t *testing.T) *signInFixture {
	t.Helper()
	passwords := authsvc.NewService(newHasher())
	revoker, watermarks, _, _ := newObservedRevoker(t, time.Now())
	stored := &recordingRefreshStore{}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{Passwords: passwords, Refresh: stored, Revoker: revoker})
	require.NoError(t, err)
	f := &signInFixture{
		passwords:  passwords,
		users:      &signInUsers{byEmail: map[string]*models.User{}, byID: map[uuid.UUID]*models.User{}},
		accounts:   &signInAccountsFake{},
		welcome:    &recordingWelcome{},
		refresh:    &rotatingRefresh{rotated: true},
		stored:     stored,
		watermarks: watermarks,
		twoFactor:  newTwoFactorFixture(t),
	}
	f.signIn, err = authsvc.NewSignIn(authsvc.SignInDeps{
		Users:     f.users,
		Accounts:  f.accounts,
		Welcome:   f.welcome,
		Passwords: passwords,
		TwoFactor: f.twoFactor.login,
		Sessions:  issuer,
		Refresh:   f.refresh,
		Revoker:   revoker,
	})
	require.NoError(t, err)
	return f
}

func (f *signInFixture) seed(t *testing.T, user models.User, password string) *models.User {
	t.Helper()
	hash, err := f.passwords.HashPassword(password)
	require.NoError(t, err)
	user.ID = uuid.New()
	user.PasswordHash = hash
	if user.Status == "" {
		user.Status = models.StatusActive
	}
	f.users.byEmail[user.Email] = &user
	f.users.byID[user.ID] = &user
	return &user
}

func TestRegister_Onboards_SendsTheWelcomeAndSignsIn(t *testing.T) {
	f := newSignIn(t)
	f.accounts.accounts = account.SignInAccounts{DefaultID: uuid.New()}
	guard := &recordingGuard{}

	registered, err := f.signIn.Register(context.Background(), guard, authsvc.RegisterInput{
		Email: "ada@example.com", Password: "a-good-password", FullName: "Ada", OrganizationName: "Acme",
	})

	require.NoError(t, err)
	require.Len(t, f.accounts.onboarded, 1)
	onboarded := f.accounts.onboarded[0]
	assert.Equal(t, "Acme", onboarded.OrganizationName)
	assert.True(t, f.passwords.CheckPassword("a-good-password", onboarded.PasswordHash), "only the hash is onboarded")
	assert.Equal(t, []uuid.UUID{registered.User.ID}, f.welcome.sent)
	assert.Equal(t, []any{registered.User.ID.String()}, guard.ids)
	assert.Equal(t, "session.jwt.token", registered.AccessToken)
	assert.Equal(t, f.accounts.accounts, registered.Accounts)
	assert.Empty(t, f.stored.stored, "registration issues no refresh token")
}

func TestRegister_Logs_AWelcomeThatFailedAndStillSignsIn(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	f := newSignIn(t)
	f.welcome.err = errors.New("mail down")

	_, err := f.signIn.Register(context.Background(), &recordingGuard{}, authsvc.RegisterInput{Email: "ada@example.com", Password: "a-good-password"})

	require.NoError(t, err)
	assert.Contains(t, logs.String(), "auth: send welcome mail failed")
}

func TestRegister_Names_TheStepThatFailed(t *testing.T) {
	outage := errors.New("store down")
	in := authsvc.RegisterInput{Email: "ada@example.com", Password: "a-good-password"}

	f := newSignIn(t)
	f.accounts.onboardErr = outage
	_, err := f.signIn.Register(context.Background(), &recordingGuard{}, in)
	assert.ErrorIs(t, err, authsvc.ErrUserNotCreated)

	f = newSignIn(t)
	f.accounts.err = outage
	guard := &recordingGuard{}
	_, err = f.signIn.Register(context.Background(), guard, in)
	assert.ErrorIs(t, err, authsvc.ErrAccountsUnavailable)
	assert.Empty(t, guard.ids, "no session without the accounts")

	f = newSignIn(t)
	_, err = f.signIn.Register(context.Background(), &recordingGuard{err: outage}, in)
	assert.ErrorIs(t, err, authsvc.ErrSessionNotCreated)
}

func TestLogin_Signs_InWithASessionAndTheAccounts(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	f.accounts.accounts = account.SignInAccounts{DefaultID: uuid.New()}

	signedIn, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: user.Email, Password: "a-good-password"})

	require.NoError(t, err)
	assert.Nil(t, signedIn.Challenge)
	assert.Equal(t, user.ID, signedIn.User.ID)
	assert.Equal(t, "session.jwt.token", signedIn.Tokens.AccessToken)
	assert.NotEmpty(t, signedIn.Tokens.RefreshToken)
	assert.Equal(t, f.accounts.accounts, signedIn.Accounts)
	assert.Len(t, f.stored.stored, 1)
}

func TestLogin_Refuses_AWrongPasswordAndAnUnknownEmailAlike(t *testing.T) {
	f := newSignIn(t)
	f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")

	_, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "ada@example.com", Password: "wrong"})
	assert.ErrorIs(t, err, authsvc.ErrInvalidCredentials)
	_, err = f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "nobody@example.com", Password: "wrong"})
	assert.ErrorIs(t, err, authsvc.ErrInvalidCredentials)
	assert.Empty(t, f.stored.stored)
}

func TestLogin_Refuses_AUserWhoMayNotHoldASession(t *testing.T) {
	f := newSignIn(t)
	f.seed(t, models.User{Email: "invited@example.com", Status: "invited"}, "a-good-password")
	suspendedAt := time.Now()
	f.seed(t, models.User{Email: "suspended@example.com", SuspendedAt: &suspendedAt}, "a-good-password")

	_, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "invited@example.com", Password: "a-good-password"})
	assert.ErrorIs(t, err, authsvc.ErrUserInactive)
	_, err = f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "suspended@example.com", Password: "a-good-password"})
	assert.ErrorIs(t, err, authsvc.ErrUserSuspended)
	assert.Empty(t, f.stored.stored)
}

func TestLogin_Tells_ALookupOutageFromABadCredential(t *testing.T) {
	f := newSignIn(t)
	f.users.err = errors.New("connection refused")

	_, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "ada@example.com", Password: "x"})

	assert.ErrorIs(t, err, authsvc.ErrUserLookup)
	assert.NotErrorIs(t, err, authsvc.ErrInvalidCredentials)
}

func TestLogin_Answers_AUserWith2FAWithAChallenge(t *testing.T) {
	f := newSignIn(t)
	f.seed(t, models.User{Email: "totp@example.com", TotpEnabled: true}, "a-good-password")

	signedIn, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "totp@example.com", Password: "a-good-password"})

	require.NoError(t, err)
	require.NotNil(t, signedIn.Challenge)
	assert.NotEmpty(t, signedIn.Challenge.Token)
	assert.Equal(t, 5*time.Minute, signedIn.Challenge.ExpiresIn)
	assert.Empty(t, f.stored.stored, "a challenge is not a session")
	assert.Zero(t, f.accounts.reads)
}

func TestLogin_Issues_NoSessionWhenTheAccountsCannotBeRead(t *testing.T) {
	f := newSignIn(t)
	f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	f.accounts.err = errors.New("membership store unavailable")

	_, err := f.signIn.Login(context.Background(), &recordingGuard{}, authsvc.LoginInput{Email: "ada@example.com", Password: "a-good-password"})

	assert.ErrorIs(t, err, authsvc.ErrAccountsUnavailable)
	assert.Empty(t, f.stored.stored)
}

func TestVerify_Needs_ACodeOrARecoveryCode(t *testing.T) {
	f := newSignIn(t)
	token := f.twoFactor.begin()

	_, err := f.signIn.Verify(context.Background(), &recordingGuard{}, authsvc.VerifyInput{ChallengeToken: token})

	assert.ErrorIs(t, err, authsvc.ErrSecondFactorMissing)
}

func TestVerify_Completes_TheChallengeWithASession(t *testing.T) {
	f := newSignIn(t)
	token := f.twoFactor.begin()

	signedIn, err := f.signIn.Verify(context.Background(), &recordingGuard{}, authsvc.VerifyInput{ChallengeToken: token, RecoveryCode: f.twoFactor.recovery[0]})

	require.NoError(t, err)
	assert.Equal(t, f.twoFactor.user.ID, signedIn.User.ID)
	assert.Equal(t, "session.jwt.token", signedIn.Tokens.AccessToken)
	assert.Len(t, f.stored.stored, 1)
}

func TestVerify_Returns_TheSecondFactorRefusalAsItIs(t *testing.T) {
	f := newSignIn(t)

	_, err := f.signIn.Verify(context.Background(), &recordingGuard{}, authsvc.VerifyInput{ChallengeToken: "unknown", Code: "123456"})

	assert.ErrorIs(t, err, authsvc.ErrChallengeInvalid)
}

func TestVerify_Refuses_AUserWhoMayNotHoldASession(t *testing.T) {
	f := newSignIn(t)
	f.twoFactor.users.byID[f.twoFactor.user.ID].Status = "invited"
	token := f.twoFactor.begin()

	_, err := f.signIn.Verify(context.Background(), &recordingGuard{}, authsvc.VerifyInput{ChallengeToken: token, RecoveryCode: f.twoFactor.recovery[0]})

	assert.ErrorIs(t, err, authsvc.ErrUserInactive)
	assert.Empty(t, f.stored.stored)
}

func (f *signInFixture) refreshToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	raw, err := f.passwords.GenerateRandomToken()
	require.NoError(t, err)
	f.refresh.tokens = append(f.refresh.tokens, models.RefreshToken{ID: uuid.New(), UserID: userID, TokenHash: f.passwords.HashToken(raw)})
	return raw
}

func TestRefresh_Rotates_TheTokenIntoANewSession(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw := f.refreshToken(t, user.ID)

	tokens, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{f.refresh.tokens[0].ID}, f.refresh.revoked)
	assert.Equal(t, "session.jwt.token", tokens.AccessToken)
	assert.NotEqual(t, raw, tokens.RefreshToken)
	assert.Len(t, f.stored.stored, 1)
}

func TestRefresh_Refuses_ATokenThatIsNotLive(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw := f.refreshToken(t, user.ID)

	_, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, "not-a-token")
	assert.ErrorIs(t, err, authsvc.ErrRefreshInvalid)

	f.refresh.rotated = false
	_, err = f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)
	assert.ErrorIs(t, err, authsvc.ErrRefreshInvalid, "a token another request already rotated")

	f.refresh.rotated = true
	f.refresh.findErr = errors.New("store down")
	f.refresh.tokens = nil
	_, err = f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)
	assert.ErrorIs(t, err, authsvc.ErrRefreshInvalid, "a failed token read is logged and answered as no match")

	f = newSignIn(t)
	raw = f.refreshToken(t, uuid.New())
	_, err = f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)
	assert.ErrorIs(t, err, authsvc.ErrRefreshInvalid, "a token whose user is gone")
	assert.Empty(t, f.stored.stored)
}

func TestRefresh_Names_TheStepThatFailed(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw := f.refreshToken(t, user.ID)
	f.refresh.rotateErr = errors.New("store down")
	_, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)
	assert.ErrorIs(t, err, authsvc.ErrSessionNotCreated)

	f = newSignIn(t)
	user = f.seed(t, models.User{Email: "ada@example.com"}, "a-good-password")
	raw = f.refreshToken(t, user.ID)
	f.users.idErr = models.ErrRepositoryNotFound
	_, err = f.signIn.Refresh(context.Background(), &recordingGuard{}, raw)
	assert.ErrorIs(t, err, authsvc.ErrSessionNotCreated, "a failed owner read is the session's failure, as it was")
}

func TestRefresh_Refuses_AnOwnerWhoMayNotHoldASession(t *testing.T) {
	f := newSignIn(t)
	user := f.seed(t, models.User{Email: "invited@example.com", Status: "invited"}, "a-good-password")
	_, err := f.signIn.Refresh(context.Background(), &recordingGuard{}, f.refreshToken(t, user.ID))
	assert.ErrorIs(t, err, authsvc.ErrUserInactive)

	suspendedAt := time.Now()
	user = f.seed(t, models.User{Email: "suspended@example.com", SuspendedAt: &suspendedAt}, "a-good-password")
	_, err = f.signIn.Refresh(context.Background(), &recordingGuard{}, f.refreshToken(t, user.ID))
	assert.ErrorIs(t, err, authsvc.ErrUserSuspended)
	assert.Empty(t, f.stored.stored)
}

func TestLogout_Ends_EverySessionOfTheUser(t *testing.T) {
	f := newSignIn(t)
	userID := uuid.New()

	require.NoError(t, f.signIn.Logout(context.Background(), userID))

	assert.Contains(t, f.watermarks.at, userID)
}

func TestNew_Sign_InRequiresEveryDependency(t *testing.T) {
	_, err := authsvc.NewSignIn(authsvc.SignInDeps{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "sign in"))
}
