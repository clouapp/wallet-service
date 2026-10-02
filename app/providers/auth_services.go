package providers

import (
	"fmt"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const (
	defaultTwoFactorChallengeTTLSeconds  = 300
	defaultTwoFactorMaxAttempts          = 5
	defaultTwoFactorAttemptWindowSeconds = 900
)

func wireAuthServices(c *container.Container) error {
	cfg := facades.Config()
	challengeTTL := time.Duration(cfg.GetInt("auth.two_factor.challenge_ttl_seconds", defaultTwoFactorChallengeTTLSeconds)) * time.Second
	attemptWindow := time.Duration(cfg.GetInt("auth.two_factor.attempt_window_seconds", defaultTwoFactorAttemptWindowSeconds)) * time.Second
	maxAttempts := cfg.GetInt("auth.two_factor.max_attempts", defaultTwoFactorMaxAttempts)

	challenges, err := authsvc.NewCacheTOTPChallengeStore(facades.Cache(), challengeTTL)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	attempts, err := authsvc.NewCacheAttemptLimiter(facades.Cache(), attemptWindow)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.NewService(), c.UserRepo, c.TotpRecoveryCodeRepo, facades.Crypt().DecryptString)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	login, err := authsvc.NewTwoFactorLogin(challenges, attempts, verifier, c.UserRepo, maxAttempts)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}

	revoker, err := authsvc.NewSessionRevoker(c.UserRepo, c.RefreshTokenRepo)
	if err != nil {
		return fmt.Errorf("vault: session revoker: %w", err)
	}

	c.SecondFactor = verifier
	c.TwoFactorLogin = login
	c.SessionRevoker = revoker
	return nil
}
