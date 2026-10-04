package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrChallengeInvalid covers an unknown, expired, spent or revoked
	// challenge. Which of those applies is not told to the caller.
	ErrChallengeInvalid = errors.New("auth: invalid or expired 2fa challenge")
	// ErrInvalidSecondFactor is a wrong, replayed or already-spent code.
	ErrInvalidSecondFactor = errors.New("auth: invalid 2fa code")
	// ErrSecondFactorLocked means the attempt cap for the window is spent.
	ErrSecondFactorLocked = errors.New("auth: too many 2fa attempts")
	// ErrSecondFactorNotEnrolled means the user has no confirmed TOTP.
	ErrSecondFactorNotEnrolled = errors.New("auth: 2fa is not enabled")
)

// UserFinder loads the user a challenge stands for.
type UserFinder interface {
	FindByID(id uuid.UUID) (*models.User, error)
}

// TotpCounterStore persists the last redeemed TOTP step per user. Login and
// withdrawal share that one step.
type TotpCounterStore interface {
	// SealedTotp is the enc:v1: secret and the last redeemed step. An empty
	// secret means there is nothing to open.
	SealedTotp(userID uuid.UUID) (secret string, counter int64, err error)
	// AdvanceTotpCounter stores counter only when it is newer than the
	// stored one and reports whether it did.
	AdvanceTotpCounter(userID uuid.UUID, counter int64) (bool, error)
}

// RecoveryCodeStore reads and spends a user's recovery codes.
type RecoveryCodeStore interface {
	FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error)
	MarkUsedIfUnused(id uuid.UUID) (bool, error)
}

// SecretDecrypter opens the TOTP secret. A value without the enc:v1: marker
// is refused. The plaintext is not returned in the error.
type SecretDecrypter func(ciphertext string) (string, error)

// SecondFactorVerifier checks a TOTP or recovery code for a user, with replay
// protection. It never issues tokens.
type SecondFactorVerifier struct {
	service  *Service
	counters TotpCounterStore
	recovery RecoveryCodeStore
	decrypt  SecretDecrypter
	now      func() time.Time
}

func NewSecondFactorVerifier(service *Service, counters TotpCounterStore, recovery RecoveryCodeStore, decrypt SecretDecrypter) (*SecondFactorVerifier, error) {
	if service == nil || counters == nil || recovery == nil || decrypt == nil {
		return nil, errors.New("auth: second factor verifier: all dependencies are required")
	}
	return &SecondFactorVerifier{service: service, counters: counters, recovery: recovery, decrypt: decrypt, now: time.Now}, nil
}

// WithClock replaces the time source; tests use it to land on a known step.
func (v *SecondFactorVerifier) WithClock(now func() time.Time) *SecondFactorVerifier {
	clone := *v
	clone.now = now
	return &clone
}

// Verify accepts a TOTP code, falling back to a recovery code when the TOTP
// code is absent or wrong. Infrastructure failures are returned wrapped and
// are distinct from ErrInvalidSecondFactor.
func (v *SecondFactorVerifier) Verify(user *models.User, code, recoveryCode string) error {
	if user == nil {
		return errors.New("auth: verify second factor: user is required")
	}
	if !user.TotpEnabled {
		return ErrSecondFactorNotEnrolled
	}
	if code != "" {
		err := v.verifyTOTP(user, code)
		if err == nil || !errors.Is(err, ErrInvalidSecondFactor) || recoveryCode == "" {
			return err
		}
	}
	if recoveryCode != "" {
		return v.verifyRecoveryCode(user, recoveryCode)
	}
	return ErrInvalidSecondFactor
}

// RecordConfirmedCode marks the step of an already-validated enrollment code
// as redeemed, so the code that confirmed TOTP cannot also complete a login.
func (v *SecondFactorVerifier) RecordConfirmedCode(userID uuid.UUID, plaintextSecret, code string) (bool, error) {
	step, ok := v.service.MatchTOTP(plaintextSecret, code, v.now())
	if !ok {
		return false, nil
	}
	if _, err := v.counters.AdvanceTotpCounter(userID, step); err != nil {
		return false, fmt.Errorf("auth: record confirmed totp step: %w", err)
	}
	return true, nil
}

// OpenSecret decrypts the stored TOTP secret. An empty plaintext and a nil
// error means enrollment has not stored one. An unsealed value fails closed.
func (v *SecondFactorVerifier) OpenSecret(userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", errors.New("auth: open totp secret: user is required")
	}
	sealed, _, err := v.counters.SealedTotp(userID)
	if err != nil {
		return "", fmt.Errorf("auth: load totp secret: %w", err)
	}
	if sealed == "" {
		return "", nil
	}
	plaintext, err := v.decrypt(sealed)
	if err != nil {
		return "", fmt.Errorf("auth: decrypt totp secret: %w", err)
	}
	return plaintext, nil
}

func (v *SecondFactorVerifier) verifyTOTP(user *models.User, code string) error {
	sealed, counter, err := v.counters.SealedTotp(user.ID)
	if err != nil {
		return fmt.Errorf("auth: load totp secret: %w", err)
	}
	if sealed == "" {
		return ErrSecondFactorNotEnrolled
	}
	secret, err := v.decrypt(sealed)
	if err != nil {
		return fmt.Errorf("auth: decrypt totp secret: %w", err)
	}
	step, ok := v.service.MatchTOTP(secret, code, v.now())
	if !ok || step <= counter {
		return ErrInvalidSecondFactor
	}
	advanced, err := v.counters.AdvanceTotpCounter(user.ID, step)
	if err != nil {
		return fmt.Errorf("auth: record totp step: %w", err)
	}
	if !advanced {
		return ErrInvalidSecondFactor
	}
	return nil
}

func (v *SecondFactorVerifier) verifyRecoveryCode(user *models.User, recoveryCode string) error {
	codes, err := v.recovery.FindUnusedByUserID(user.ID)
	if err != nil {
		return fmt.Errorf("auth: load recovery codes: %w", err)
	}
	for _, candidate := range codes {
		if !v.service.VerifyRecoveryCode(recoveryCode, candidate.CodeHash) {
			continue
		}
		spent, err := v.recovery.MarkUsedIfUnused(candidate.ID)
		if err != nil {
			return fmt.Errorf("auth: spend recovery code: %w", err)
		}
		if !spent {
			return ErrInvalidSecondFactor
		}
		return nil
	}
	return ErrInvalidSecondFactor
}

// TwoFactorChallenge is what the password step hands back instead of a session.
type TwoFactorChallenge struct {
	Token     string
	ExpiresIn time.Duration
}

// TwoFactorLogin is the second step of a password login for users with TOTP:
// Begin replaces the session the password alone used to earn with a
// challenge, and Complete turns a challenge plus a valid code into the user a
// session may be issued for.
type TwoFactorLogin struct {
	challenges  TOTPChallengeStore
	attempts    AttemptLimiter
	verifier    *SecondFactorVerifier
	users       UserFinder
	maxAttempts int64
}

func NewTwoFactorLogin(challenges TOTPChallengeStore, attempts AttemptLimiter, verifier *SecondFactorVerifier, users UserFinder, maxAttempts int) (*TwoFactorLogin, error) {
	if challenges == nil || attempts == nil || verifier == nil || users == nil {
		return nil, errors.New("auth: two factor login: all dependencies are required")
	}
	if maxAttempts < 1 {
		return nil, fmt.Errorf("auth: two factor login: max attempts must be at least 1, got %d", maxAttempts)
	}
	return &TwoFactorLogin{
		challenges:  challenges,
		attempts:    attempts,
		verifier:    verifier,
		users:       users,
		maxAttempts: int64(maxAttempts),
	}, nil
}

func (l *TwoFactorLogin) Begin(user *models.User) (TwoFactorChallenge, error) {
	if user == nil || user.ID == uuid.Nil {
		return TwoFactorChallenge{}, errors.New("auth: begin 2fa: user is required")
	}
	token, err := l.challenges.Issue(user.ID)
	if err != nil {
		return TwoFactorChallenge{}, err
	}
	return TwoFactorChallenge{Token: token, ExpiresIn: l.challenges.TTL()}, nil
}

// Complete resolves the challenge, enforces the attempt cap, checks the code
// and spends the challenge. A wrong code leaves the challenge usable; the cap
// or a successful answer retires it.
func (l *TwoFactorLogin) Complete(token, code, recoveryCode string) (*models.User, error) {
	user, err := l.challengedUser(token)
	if err != nil {
		return nil, err
	}

	attempt, err := l.attempts.Claim(user.ID)
	if err != nil {
		return nil, err
	}
	if attempt > l.maxAttempts {
		l.challenges.Revoke(token)
		return nil, ErrSecondFactorLocked
	}

	if err := l.verifier.Verify(user, code, recoveryCode); err != nil {
		if errors.Is(err, ErrSecondFactorNotEnrolled) {
			l.challenges.Revoke(token)
			return nil, ErrChallengeInvalid
		}
		return nil, err
	}

	if !l.challenges.Consume(token) {
		return nil, ErrChallengeInvalid
	}
	l.attempts.Reset(user.ID)
	return user, nil
}

func (l *TwoFactorLogin) challengedUser(token string) (*models.User, error) {
	challenge, ok, err := l.challenges.Resolve(token)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrChallengeInvalid
	}
	user, err := l.users.FindByID(challenge.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth: load challenged user: %w", err)
	}
	if user == nil || !user.TotpEnabled || SessionRevoked(challenge.IssuedAt, user.SessionsRevokedAt) {
		l.challenges.Revoke(token)
		return nil, ErrChallengeInvalid
	}
	return user, nil
}
