// Package otp issues and redeems the one-time codes behind email verification,
// password reset and login, for any authenticatable subject.
//
// Admins and users ran separate copies of this logic — same generation, same
// invalidate-then-issue, same expiry check — differing only in which table they
// wrote to and which template they mailed. Those are the two things this
// service takes as dependencies.
package otp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// Mailer queues the message carrying a freshly issued code.
//
// It is the seam between the shared mechanics and the per-audience copy: an
// admin's login code and a client's do not read the same, and neither should
// this package have to know that.
type Mailer interface {
	Send(subject identity.Subject, purpose identity.OTPPurpose, code string) error
}

// Issuer is what the auth services consume. It is declared here rather than in
// each consumer because both want exactly the same set, and two identical
// interface declarations drift the moment one of them grows a method.
//
// *Service is the only implementation; the interface exists so the flows above
// can be tested against "a code was issued for this purpose" instead of
// re-asserting the invalidate/create/mail mechanics this package already
// covers.
type Issuer interface {
	Request(email string, purpose identity.OTPPurpose, subject identity.Subject, opts RequestOptions) (Receipt, error)
	Check(ctx context.Context, subject identity.Subject, guess string, purpose identity.OTPPurpose, maxGuesses int) (*models.OTPCode, error)
	MarkUsed(code *models.OTPCode) error
}

// Receipt is what a caller may tell the client about a send.
type Receipt struct {
	CodeSent    bool
	ExpiresIn   time.Duration
	ResendAfter time.Duration
}

// RequestOptions bends Request for the two flows that need it.
type RequestOptions struct {
	// Force issues during a running cooldown and restarts it. Only for a send
	// the request itself proves is the first — a brand-new registration — so a
	// stranger pre-claiming the address cannot leave the account codeless.
	Force bool
	// BeforeIssue runs once the cooldown is claimed and before the code exists.
	BeforeIssue func() error
}

// Service satisfies Issuer.
var _ Issuer = (*Service)(nil)

// IssuesPerWindow codes of one purpose per subject per IssueWindow, whoever
// asks: the routes' limiters are keyed by an address an attacker can change,
// and each code brings its own set of guesses.
const (
	IssuesPerWindow = 5
	IssueWindow     = 15 * time.Minute
)

// Service issues and verifies one-time codes.
type Service struct {
	repo      repositories.OTPCodeRepository
	mailer    Mailer
	audience  string
	policy    Policy
	cooldowns CooldownStore
	guesses   GuessCounter
}

// NewService wires the service. audience is the subject type whose codes it
// issues, and scopes its cooldowns.
func NewService(
	repo repositories.OTPCodeRepository,
	mailer Mailer,
	audience string,
	policy Policy,
	cooldowns CooldownStore,
	guesses GuessCounter,
) *Service {
	return &Service{
		repo:      repo,
		mailer:    mailer,
		audience:  audience,
		policy:    policy,
		cooldowns: cooldowns,
		guesses:   guesses,
	}
}

// Request claims the resend cooldown for (purpose, email) and, when subject is
// known, issues a code.
//
// The claim is made whether or not the address belongs to anyone, so an
// unknown address is refused and answered exactly like a known one — the
// silent endpoints stay silent. subject must be an untyped nil for an unknown
// address.
func (s *Service) Request(email string, purpose identity.OTPPurpose, subject identity.Subject, opts RequestOptions) (Receipt, error) {
	cooldown := s.policy.ResendCooldown()
	key := cooldownKey(s.audience, purpose, email)
	if err := s.claim(key, cooldown, opts.Force); err != nil {
		return Receipt{}, err
	}
	ttl := s.policy.TTL(purpose)
	receipt := Receipt{CodeSent: true, ExpiresIn: ttl, ResendAfter: cooldown}
	if subject == nil {
		return receipt, nil
	}
	if opts.BeforeIssue != nil {
		if err := opts.BeforeIssue(); err != nil {
			s.releaseCooldown(key)
			return Receipt{}, err
		}
	}
	sent, err := s.issue(subject, purpose, ttl)
	if err != nil {
		s.releaseCooldown(key)
		return Receipt{}, err
	}
	if !sent {
		receipt.CodeSent = false
		receipt.ExpiresIn = 0
	}
	return receipt, nil
}

func (s *Service) claim(key string, d time.Duration, force bool) error {
	if force {
		return s.cooldowns.Restart(key, d)
	}
	left, ok, err := s.cooldowns.Claim(key, d)
	if err != nil {
		return fmt.Errorf("otp: claim cooldown: %w", err)
	}
	if !ok {
		return &CooldownError{RetryAfter: left}
	}
	return nil
}

func (s *Service) releaseCooldown(key string) {
	if rerr := s.cooldowns.Release(key); rerr != nil {
		facades.Log().Warningf("otp: release cooldown after failed send: %v", rerr)
	}
}

// issue starts a fresh guess count, retires the subject's outstanding codes for
// this purpose, stores a fresh one, and mails it.
//
// The order is load-bearing. The count starts before the code exists: a reset
// that failed after the email went out would leave the subject holding a fresh
// code against a spent count, and one that cannot start at all would send the
// code out uncapped. It is kept as long as the code lives, because a count
// that lapsed first would restart at zero, with no expiry, and give the same
// code a new set of guesses. Retiring before creating means a code already
// mailed cannot be redeemed once a newer one exists. Mailing last means the
// subject never holds a code the database does not, which would read to them
// as a login that rejects a code they can see.
func (s *Service) issue(subject identity.Subject, purpose identity.OTPPurpose, ttl time.Duration) (bool, error) {
	// Past the cap the last code mailed stays the live one, and the caller
	// answers as if a new one went out.
	issued, err := s.guesses.Issued(subject, purpose)
	if err != nil {
		return false, fmt.Errorf("otp: count issued codes: %w", err)
	}
	if issued > IssuesPerWindow {
		facades.Log().With(map[string]any{
			"subject_type": subject.SubjectType(), "subject_id": subject.SubjectID(), "purpose": purpose,
		}).Warning("otp: issue cap reached, no code sent")
		return false, nil
	}
	if err := s.guesses.Reset(subject, purpose, ttl); err != nil {
		return false, fmt.Errorf("otp: start guess count: %w", err)
	}
	if err := s.repo.InvalidateUnused(subject, purpose); err != nil {
		return false, fmt.Errorf("otp: invalidate outstanding: %w", err)
	}
	code, err := generateOTP()
	if err != nil {
		return false, fmt.Errorf("otp: generate: %w", err)
	}
	if err := s.repo.Create(&models.OTPCode{
		SubjectType: subject.SubjectType(),
		SubjectID:   subject.SubjectID(),
		Purpose:     purpose,
		Code:        code,
		ExpiresAt:   time.Now().Add(ttl),
	}); err != nil {
		return false, fmt.Errorf("otp: create: %w", err)
	}
	if err := s.mailer.Send(subject, purpose, code); err != nil {
		return false, fmt.Errorf("otp: send: %w", err)
	}
	return true, nil
}

// Check reads the subject's outstanding code of the purpose and checks guess
// against it, in constant time. The right code comes back unspent, so the
// caller decides when to retire it. Every other outcome is nil: no live code,
// a wrong guess, a code just burned. Telling them apart would tell a caller
// what the holder of the right code never needs to ask.
//
// Every guess is counted per code before it is compared, and only the first
// maxGuesses are compared at all: the maxGuesses-th wrong one, or any guess past
// it, burns the code, so the right code afterwards is worth nothing and the
// subject asks for a new one. Counting first is what makes the cap hold under
// concurrency. A count taken after the comparison let every request already
// past the lookup compare, however many guesses were on the count, until the
// burn landed. The routes' limiters are keyed by an address, which an
// attacker can change; this is what bounds the guesses at one code. A count
// that cannot be written is a cap that is not being enforced, so that is an
// error, not a wrong guess.
func (s *Service) Check(ctx context.Context, subject identity.Subject, guess string, purpose identity.OTPPurpose, maxGuesses int) (*models.OTPCode, error) {
	code, err := s.repo.FindActive(subject, purpose)
	if err != nil {
		return nil, fmt.Errorf("otp: check %s: %w", purpose, err)
	}
	if code == nil {
		return nil, nil
	}
	count, err := s.guesses.Claim(subject, purpose)
	if err != nil {
		return nil, fmt.Errorf("otp: check %s: count the guess: %w", purpose, err)
	}
	if count <= int64(maxGuesses) && matches(code.Code, guess) {
		return code, nil
	}
	if count < int64(maxGuesses) {
		return nil, nil
	}
	switch err := s.repo.MarkUsed(code); {
	case errors.Is(err, repositories.ErrOTPCodeAlreadyUsed):
		// A request that spent or burned the code first leaves this caller
		// holding a dead code, which is what nil tells them.
	case err != nil:
		return nil, fmt.Errorf("otp: check %s: burn the code at the cap: %w", purpose, err)
	default:
		facades.Log().WithContext(ctx).With(map[string]any{
			"subject_type": subject.SubjectType(), "subject_id": subject.SubjectID(), "purpose": purpose,
		}).Warning("otp: guess cap reached, code burned")
	}
	return nil, nil
}

// MarkUsed consumes a code outside a transaction.
func (s *Service) MarkUsed(code *models.OTPCode) error {
	if err := s.repo.MarkUsed(code); err != nil {
		return fmt.Errorf("otp: mark used: %w", err)
	}
	return nil
}

// matches compares two codes without leaking their divergence point through
// timing.
func matches(stored, submitted string) bool {
	if len(stored) != len(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(submitted)) == 1
}

// generateOTP returns a 6-digit cryptographically random numeric code. The
// zero-padding is not cosmetic: a bare %d would render a small draw as "42",
// collapsing the guess space from a million to a hundred.
func generateOTP() (string, error) {
	const digits = 6
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", digits, n.Int64()), nil
}
