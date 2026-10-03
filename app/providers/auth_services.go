package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

const (
	defaultTwoFactorChallengeTTLSeconds  = 300
	defaultTwoFactorMaxAttempts          = 5
	defaultTwoFactorAttemptWindowSeconds = 900
)

// authRepoBridge adapts the context-taking repositories to the auth service ports.
type authRepoBridge struct {
	users    *repositories.UserRepository
	recovery *repositories.TotpRecoveryCodeRepository
	refresh  *repositories.RefreshTokenRepository
}

func (b authRepoBridge) FindByID(id uuid.UUID) (*models.User, error) {
	return b.users.FindByID(context.Background(), id)
}

func (b authRepoBridge) AdvanceTotpCounter(id uuid.UUID, counter int64) (bool, error) {
	return b.users.AdvanceTotpCounter(context.Background(), id, counter)
}

func (b authRepoBridge) UpdateSessionsRevokedAt(id uuid.UUID, at time.Time) error {
	return b.users.UpdateSessionsRevokedAt(context.Background(), id, at)
}

func (b authRepoBridge) FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	return b.recovery.FindUnusedByUserID(context.Background(), userID)
}

func (b authRepoBridge) MarkUsedIfUnused(id uuid.UUID) (bool, error) {
	return b.recovery.MarkUsedIfUnused(context.Background(), id)
}

func (b authRepoBridge) RevokeAllForUser(userID uuid.UUID) error {
	return b.refresh.RevokeAllForUser(context.Background(), userID)
}

func wireAuthServices(c *container.Container) error {
	cfg := appfacades.Config()
	challengeTTL := time.Duration(cfg.GetInt("auth.two_factor.challenge_ttl_seconds", defaultTwoFactorChallengeTTLSeconds)) * time.Second
	attemptWindow := time.Duration(cfg.GetInt("auth.two_factor.attempt_window_seconds", defaultTwoFactorAttemptWindowSeconds)) * time.Second
	maxAttempts := cfg.GetInt("auth.two_factor.max_attempts", defaultTwoFactorMaxAttempts)

	challenges, err := authsvc.NewCacheTOTPChallengeStore(appfacades.Cache(), challengeTTL)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	attempts, err := authsvc.NewCacheAttemptLimiter(appfacades.Cache(), attemptWindow)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	bridge := authRepoBridge{users: c.UserRepo, recovery: c.TotpRecoveryCodeRepo, refresh: c.RefreshTokenRepo}
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.NewService(), bridge, bridge, appfacades.Crypt().DecryptString)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	login, err := authsvc.NewTwoFactorLogin(challenges, attempts, verifier, bridge, maxAttempts)
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	revoker, err := authsvc.NewSessionRevoker(bridge, bridge)
	if err != nil {
		return fmt.Errorf("vault: session revoker: %w", err)
	}

	c.SecondFactor = verifier
	c.TwoFactorLogin = login
	c.SessionRevoker = revoker
	return nil
}
