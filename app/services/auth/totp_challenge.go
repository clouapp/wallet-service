package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	contractscache "github.com/goravel/framework/contracts/cache"
)

const (
	challengeTokenBytes     = 32
	challengeKeyPrefix      = "auth:2fa:challenge:"
	challengeSpentKeyPrefix = "auth:2fa:challenge-spent:"
	attemptsKeyPrefix       = "auth:2fa:attempts:"
)

// TOTPChallengeStore carries "the password was right" from the login request
// to the request that answers the second factor.
//
// The token is opaque and is not a session JWT, so SessionAuth can never
// accept it. Resolving does not spend it: a mistyped digit must not cost the
// whole challenge, otherwise the attempt cap would be unreachable. Consume is
// the atomic spend that guarantees one challenge never yields two sessions.
type TOTPChallengeStore interface {
	Issue(userID uuid.UUID) (string, error)
	// Resolve returns what a live challenge stands for; ok is false when the
	// token is unknown, expired or already spent.
	Resolve(token string) (challenge TOTPChallenge, ok bool, err error)
	// Consume spends the challenge; only the first caller gets true.
	Consume(token string) bool
	// Revoke drops the challenge. Revoking an absent token is not an error.
	Revoke(token string)
	TTL() time.Duration
}

// TOTPChallenge is the user a challenge was issued to and when. IssuedAt has
// second precision, like a JWT iat, so both compare to the session watermark
// the same way.
type TOTPChallenge struct {
	UserID   uuid.UUID
	IssuedAt time.Time
}

type storedChallenge struct {
	UserID   string `json:"user_id"`
	IssuedAt int64  `json:"issued_at"`
}

// AttemptLimiter bounds second-factor guesses per user within a window.
type AttemptLimiter interface {
	// Claim takes one attempt before the code is checked and returns the
	// attempt number. Claiming first, atomically, is what stops concurrent
	// guesses from all reading the same pre-increment count.
	Claim(userID uuid.UUID) (int64, error)
	Reset(userID uuid.UUID)
}

// CacheTOTPChallengeStore keeps challenges in the cache (Redis in every
// environment but tests that pick the memory store). Keys hold a SHA-256 of
// the token, so a cache dump does not yield usable tokens.
type CacheTOTPChallengeStore struct {
	cache contractscache.Driver
	ttl   time.Duration
}

// ChallengeStoreDeps is everything the cache TOTP challenge store uses.
// Cache and TTL are required.
type ChallengeStoreDeps struct {
	Cache contractscache.Driver
	TTL   time.Duration
}

func NewCacheTOTPChallengeStore(deps ChallengeStoreDeps) (*CacheTOTPChallengeStore, error) {
	if deps.Cache == nil {
		return nil, errors.New("auth: totp challenge store needs a cache driver")
	}
	if deps.TTL <= 0 {
		return nil, fmt.Errorf("auth: totp challenge ttl must be positive, got %s", deps.TTL)
	}
	return &CacheTOTPChallengeStore{cache: deps.Cache, ttl: deps.TTL}, nil
}

func (s *CacheTOTPChallengeStore) TTL() time.Duration { return s.ttl }

func (s *CacheTOTPChallengeStore) Issue(userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", errors.New("auth: issue totp challenge: user id is required")
	}
	raw := make([]byte, challengeTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: issue totp challenge: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	payload, err := json.Marshal(storedChallenge{UserID: userID.String(), IssuedAt: time.Now().Unix()})
	if err != nil {
		return "", fmt.Errorf("auth: encode totp challenge: %w", err)
	}
	if err := s.cache.Put(challengeKey(token), string(payload), s.ttl); err != nil {
		return "", fmt.Errorf("auth: store totp challenge: %w", err)
	}
	return token, nil
}

func (s *CacheTOTPChallengeStore) Resolve(token string) (TOTPChallenge, bool, error) {
	if token == "" {
		return TOTPChallenge{}, false, nil
	}
	raw := s.cache.GetString(challengeKey(token), "")
	if raw == "" {
		return TOTPChallenge{}, false, nil
	}
	var stored storedChallenge
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return TOTPChallenge{}, false, fmt.Errorf("auth: decode totp challenge: %w", err)
	}
	userID, err := uuid.Parse(stored.UserID)
	if err != nil {
		return TOTPChallenge{}, false, fmt.Errorf("auth: decode totp challenge: stored value is not a user id: %w", err)
	}
	return TOTPChallenge{UserID: userID, IssuedAt: time.Unix(stored.IssuedAt, 0)}, true, nil
}

func (s *CacheTOTPChallengeStore) Consume(token string) bool {
	if token == "" {
		return false
	}
	if !s.cache.Add(challengeSpentKey(token), true, s.ttl) {
		return false
	}
	s.cache.Forget(challengeKey(token))
	return true
}

func (s *CacheTOTPChallengeStore) Revoke(token string) {
	if token == "" {
		return
	}
	s.cache.Forget(challengeKey(token))
}

func challengeKey(token string) string      { return challengeKeyPrefix + hashToken(token) }
func challengeSpentKey(token string) string { return challengeSpentKeyPrefix + hashToken(token) }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CacheAttemptLimiter counts second-factor attempts per user in the cache.
// The window starts at the first attempt and is not extended by later ones,
// so a locked-out user can try again once it lapses.
type CacheAttemptLimiter struct {
	cache  contractscache.Driver
	window time.Duration
}

// AttemptLimiterDeps is everything the cache attempt limiter uses.
// Cache and Window are required.
type AttemptLimiterDeps struct {
	Cache  contractscache.Driver
	Window time.Duration
}

func NewCacheAttemptLimiter(deps AttemptLimiterDeps) (*CacheAttemptLimiter, error) {
	if deps.Cache == nil {
		return nil, errors.New("auth: attempt limiter needs a cache driver")
	}
	if deps.Window <= 0 {
		return nil, fmt.Errorf("auth: attempt window must be positive, got %s", deps.Window)
	}
	return &CacheAttemptLimiter{cache: deps.Cache, window: deps.Window}, nil
}

func (l *CacheAttemptLimiter) Claim(userID uuid.UUID) (int64, error) {
	if userID == uuid.Nil {
		return 0, errors.New("auth: claim attempt: user id is required")
	}
	key := attemptsKey(userID)
	if l.cache.Add(key, 1, l.window) {
		return 1, nil
	}
	attempt, err := l.cache.Increment(key)
	if err != nil {
		return 0, fmt.Errorf("auth: claim attempt: %w", err)
	}
	// The key lapsed between Add and Increment, and INCRBY recreated it
	// without an expiry; give it one so the lockout cannot become permanent.
	if attempt == 1 {
		if err := l.cache.Put(key, 1, l.window); err != nil {
			return 0, fmt.Errorf("auth: claim attempt: %w", err)
		}
	}
	return attempt, nil
}

func (l *CacheAttemptLimiter) Reset(userID uuid.UUID) {
	l.cache.Forget(attemptsKey(userID))
}

func attemptsKey(userID uuid.UUID) string { return attemptsKeyPrefix + userID.String() }
