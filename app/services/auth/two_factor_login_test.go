package auth_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const (
	sealedPrefix    = "sealed:"
	testMaxAttempts = 3
)

// ---- fakes for the ports ----

type fakeChallengeStore struct {
	mu       sync.Mutex
	live     map[string]authsvc.TOTPChallenge
	spent    map[string]bool
	issuedAt time.Time
}

func newFakeChallengeStore(issuedAt time.Time) *fakeChallengeStore {
	return &fakeChallengeStore{live: map[string]authsvc.TOTPChallenge{}, spent: map[string]bool{}, issuedAt: issuedAt}
}

func (f *fakeChallengeStore) Issue(userID uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := uuid.NewString()
	f.live[token] = authsvc.TOTPChallenge{UserID: userID, IssuedAt: f.issuedAt}
	return token, nil
}

func (f *fakeChallengeStore) Resolve(token string) (authsvc.TOTPChallenge, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	challenge, ok := f.live[token]
	return challenge, ok, nil
}

func (f *fakeChallengeStore) Consume(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.spent[token] {
		return false
	}
	f.spent[token] = true
	delete(f.live, token)
	return true
}

func (f *fakeChallengeStore) Revoke(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, token)
}

func (f *fakeChallengeStore) TTL() time.Duration { return 5 * time.Minute }

// expire simulates the TTL lapsing.
func (f *fakeChallengeStore) expire(token string) { f.Revoke(token) }

type fakeAttemptLimiter struct {
	mu       sync.Mutex
	attempts map[uuid.UUID]int64
}

func newFakeAttemptLimiter() *fakeAttemptLimiter {
	return &fakeAttemptLimiter{attempts: map[uuid.UUID]int64{}}
}

func (f *fakeAttemptLimiter) Claim(userID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[userID]++
	return f.attempts[userID], nil
}

func (f *fakeAttemptLimiter) Reset(userID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attempts, userID)
}

type fakeUsers struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*models.User
	recovery map[uuid.UUID][]models.TotpRecoveryCode
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byID: map[uuid.UUID]*models.User{}, recovery: map[uuid.UUID][]models.TotpRecoveryCode{}}
}

func (f *fakeUsers) FindByID(id uuid.UUID) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	clone := *user
	return &clone, nil
}

func (f *fakeUsers) SealedTotp(userID uuid.UUID) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.byID[userID]
	if !ok {
		return "", 0, nil
	}
	return user.TotpSecret, user.TotpLastUsedCounter, nil
}

func (f *fakeUsers) AdvanceTotpCounter(userID uuid.UUID, counter int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.byID[userID]
	if !ok || counter <= user.TotpLastUsedCounter {
		return false, nil
	}
	user.TotpLastUsedCounter = counter
	return true, nil
}

func (f *fakeUsers) FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var unused []models.TotpRecoveryCode
	for _, code := range f.recovery[userID] {
		if code.UsedAt == nil {
			unused = append(unused, code)
		}
	}
	return unused, nil
}

func (f *fakeUsers) MarkUsedIfUnused(id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for userID, codes := range f.recovery {
		for i := range codes {
			if codes[i].ID != id {
				continue
			}
			if codes[i].UsedAt != nil {
				return false, nil
			}
			now := time.Now()
			f.recovery[userID][i].UsedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func fakeDecrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, sealedPrefix) {
		return "", errors.New("not sealed")
	}
	return strings.TrimPrefix(ciphertext, sealedPrefix), nil
}

// ---- fixture ----

type twoFactorFixture struct {
	t          *testing.T
	now        time.Time
	secret     string
	user       *models.User
	users      *fakeUsers
	challenges *fakeChallengeStore
	attempts   *fakeAttemptLimiter
	login      *authsvc.TwoFactorLogin
	recovery   []string
}

func newTwoFactorFixture(t *testing.T) *twoFactorFixture {
	t.Helper()
	svc := authsvc.NewService()
	secret := newTOTPSecret(t)
	now := time.Unix(1_700_000_010, 0)

	user := &models.User{ID: uuid.New(), Email: "totp@example.com", TotpEnabled: true, TotpSecret: sealedPrefix + secret, Status: "active"}
	users := newFakeUsers()
	users.byID[user.ID] = user

	plainCodes, hashes, err := svc.GenerateRecoveryCodes()
	require.NoError(t, err)
	for _, hash := range hashes[:2] {
		users.recovery[user.ID] = append(users.recovery[user.ID], models.TotpRecoveryCode{ID: uuid.New(), UserID: user.ID, CodeHash: hash})
	}

	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  svc,
		Counters: users,
		Recovery: users,
		Decrypt:  fakeDecrypt,
		Now:      func() time.Time { return now },
	})
	require.NoError(t, err)

	challenges := newFakeChallengeStore(now)
	attempts := newFakeAttemptLimiter()
	login, err := authsvc.NewTwoFactorLogin(challenges, attempts, verifier, users, testMaxAttempts)
	require.NoError(t, err)

	return &twoFactorFixture{
		t: t, now: now, secret: secret, user: user, users: users,
		challenges: challenges, attempts: attempts, login: login, recovery: plainCodes[:2],
	}
}

func (f *twoFactorFixture) begin() string {
	f.t.Helper()
	challenge, err := f.login.Begin(f.user)
	require.NoError(f.t, err)
	require.NotEmpty(f.t, challenge.Token)
	return challenge.Token
}

func (f *twoFactorFixture) validCode() string {
	return codeAt(f.t, f.secret, f.now)
}

// ---- tests ----

func TestTwoFactorLogin_BeginIssuesAChallengeNotASession(t *testing.T) {
	f := newTwoFactorFixture(t)

	challenge, err := f.login.Begin(f.user)

	require.NoError(t, err)
	require.Equal(t, 5*time.Minute, challenge.ExpiresIn)
	resolved, ok, err := f.challenges.Resolve(challenge.Token)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, f.user.ID, resolved.UserID)
}

func TestTwoFactorLogin_ChallengeIssuedBeforeASessionRevocationIsRefused(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()
	watermark := f.now.Add(time.Second)
	f.users.byID[f.user.ID].SessionsRevokedAt = &watermark

	_, err := f.login.Complete(token, f.validCode(), "")

	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid)
	_, ok, _ := f.challenges.Resolve(token)
	require.False(t, ok, "the stale challenge is retired")
}

func TestTwoFactorLogin_ChallengeIssuedAfterTheWatermarkStillWorks(t *testing.T) {
	f := newTwoFactorFixture(t)
	watermark := f.now
	f.users.byID[f.user.ID].SessionsRevokedAt = &watermark

	_, err := f.login.Complete(f.begin(), f.validCode(), "")

	require.NoError(t, err)
}

func TestTwoFactorLogin_ValidTOTPCompletesAndSpendsTheChallenge(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()

	user, err := f.login.Complete(token, f.validCode(), "")

	require.NoError(t, err)
	require.Equal(t, f.user.ID, user.ID)
	_, err = f.login.Complete(token, f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid, "a challenge yields one session at most")
}

func TestTwoFactorLogin_DecryptsTheSecretBeforeValidating(t *testing.T) {
	f := newTwoFactorFixture(t)
	f.users.byID[f.user.ID].TotpSecret = "not-sealed-" + f.secret
	token := f.begin()

	_, err := f.login.Complete(token, f.validCode(), "")

	require.Error(t, err)
	require.NotErrorIs(t, err, authsvc.ErrInvalidSecondFactor, "a secret that cannot be opened is a server error, not a wrong code")
}

func TestTwoFactorLogin_ReplayedCodeIsRefused(t *testing.T) {
	f := newTwoFactorFixture(t)
	code := f.validCode()

	_, err := f.login.Complete(f.begin(), code, "")
	require.NoError(t, err)

	_, err = f.login.Complete(f.begin(), code, "")
	require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
}

func TestTwoFactorLogin_ConcurrentRedemptionsOfOneCodeYieldOneSuccess(t *testing.T) {
	f := newTwoFactorFixture(t)
	code := f.validCode()
	const racers = 8
	tokens := make([]string, racers)
	for i := range tokens {
		tokens[i] = f.begin()
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for _, token := range tokens {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			if _, err := f.login.Complete(token, code, ""); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(token)
	}
	wg.Wait()

	require.Equal(t, 1, successes)
}

func TestTwoFactorLogin_WrongCodeKeepsTheChallengeUsable(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()

	_, err := f.login.Complete(token, "000000", "")
	require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)

	user, err := f.login.Complete(token, f.validCode(), "")
	require.NoError(t, err)
	require.Equal(t, f.user.ID, user.ID)
	require.Zero(t, f.attempts.attempts[f.user.ID], "a success clears the attempt counter")
}

func TestTwoFactorLogin_AttemptCapRevokesTheChallenge(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()

	for i := 0; i < testMaxAttempts; i++ {
		_, err := f.login.Complete(token, "000000", "")
		require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
	}
	_, err := f.login.Complete(token, f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrSecondFactorLocked, "the right code after the cap is still refused")

	_, err = f.login.Complete(token, f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid, "the capped challenge is gone")
}

func TestTwoFactorLogin_CapSpansNewChallengesForTheSameUser(t *testing.T) {
	f := newTwoFactorFixture(t)
	for i := 0; i < testMaxAttempts; i++ {
		_, err := f.login.Complete(f.begin(), "000000", "")
		require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
	}

	_, err := f.login.Complete(f.begin(), f.validCode(), "")

	require.ErrorIs(t, err, authsvc.ErrSecondFactorLocked, "logging in again does not reset the guess budget")
}

func TestTwoFactorLogin_ExpiredOrUnknownChallengeIsRefused(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()
	f.challenges.expire(token)

	_, err := f.login.Complete(token, f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid)

	_, err = f.login.Complete("", f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid)

	_, err = f.login.Complete("not-a-challenge", f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid)
}

func TestTwoFactorLogin_ChallengeDiesWhenTOTPWasDisabledMeanwhile(t *testing.T) {
	f := newTwoFactorFixture(t)
	token := f.begin()
	f.users.byID[f.user.ID].TotpEnabled = false

	_, err := f.login.Complete(token, f.validCode(), "")

	require.ErrorIs(t, err, authsvc.ErrChallengeInvalid)
	_, ok, _ := f.challenges.Resolve(token)
	require.False(t, ok)
}

func TestTwoFactorLogin_RecoveryCodeCompletesOnce(t *testing.T) {
	f := newTwoFactorFixture(t)

	user, err := f.login.Complete(f.begin(), "", f.recovery[0])
	require.NoError(t, err)
	require.Equal(t, f.user.ID, user.ID)

	_, err = f.login.Complete(f.begin(), "", f.recovery[0])
	require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor, "a recovery code is single use")

	_, err = f.login.Complete(f.begin(), "", f.recovery[1])
	require.NoError(t, err, "the other recovery codes still work")
}

func TestTwoFactorLogin_RecoveryCodeIsTheFallbackForAWrongTOTP(t *testing.T) {
	f := newTwoFactorFixture(t)

	_, err := f.login.Complete(f.begin(), "000000", f.recovery[0])

	require.NoError(t, err)
}

func TestTwoFactorLogin_NoCodeAtAllIsInvalid(t *testing.T) {
	f := newTwoFactorFixture(t)

	_, err := f.login.Complete(f.begin(), "", "")

	require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
}

func TestSecondFactorVerifier_ConfirmedCodeCannotCompleteALogin(t *testing.T) {
	f := newTwoFactorFixture(t)
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  authsvc.NewService(),
		Counters: f.users,
		Recovery: f.users,
		Decrypt:  fakeDecrypt,
		Now:      func() time.Time { return f.now },
	})
	require.NoError(t, err)

	matched, err := verifier.RecordConfirmedCode(f.user.ID, f.secret, f.validCode())
	require.NoError(t, err)
	require.True(t, matched)

	_, err = f.login.Complete(f.begin(), f.validCode(), "")
	require.ErrorIs(t, err, authsvc.ErrInvalidSecondFactor)
}

func TestSecondFactorVerifier_RefusesNotEnrolledUsers(t *testing.T) {
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  authsvc.NewService(),
		Counters: newFakeUsers(),
		Recovery: newFakeUsers(),
		Decrypt:  fakeDecrypt,
	})
	require.NoError(t, err)

	err = verifier.Verify(&models.User{ID: uuid.New()}, "123456", "")
	require.ErrorIs(t, err, authsvc.ErrSecondFactorNotEnrolled)

	err = verifier.Verify(nil, "123456", "")
	require.Error(t, err)
}

func TestNewTwoFactorLogin_ValidatesDependencies(t *testing.T) {
	f := newTwoFactorFixture(t)
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  authsvc.NewService(),
		Counters: f.users,
		Recovery: f.users,
		Decrypt:  fakeDecrypt,
	})
	require.NoError(t, err)

	_, err = authsvc.NewTwoFactorLogin(nil, f.attempts, verifier, f.users, 1)
	require.Error(t, err)
	_, err = authsvc.NewTwoFactorLogin(f.challenges, f.attempts, verifier, f.users, 0)
	require.Error(t, err)
	_, err = authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  authsvc.NewService(),
		Counters: f.users,
		Recovery: f.users,
	})
	require.Error(t, err)
}
