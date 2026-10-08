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
	"github.com/macrowallets/waas/app/services/settings"
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

func (b authRepoBridge) SealedTotp(id uuid.UUID) (string, int64, error) {
	return b.users.SealedTotp(context.Background(), id)
}

func (b authRepoBridge) AdvanceTotpCounter(id uuid.UUID, counter int64) (bool, error) {
	return b.users.AdvanceTotpCounter(context.Background(), id, counter)
}

func (b authRepoBridge) UpdateSessionsRevokedAt(ctx context.Context, id uuid.UUID, at time.Time) error {
	return b.users.UpdateSessionsRevokedAt(ctx, id, at)
}

func (b authRepoBridge) FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	return b.recovery.FindUnusedByUserID(context.Background(), userID)
}

func (b authRepoBridge) MarkUsedIfUnused(id uuid.UUID) (bool, error) {
	return b.recovery.MarkUsedIfUnused(context.Background(), id)
}

func (b authRepoBridge) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	return b.refresh.RevokeAllForUser(ctx, userID)
}

func openSealedTotp(stored string) (string, error) {
	return settings.Open(appfacades.Crypt(), stored)
}

func wireAuthServices(c *container.Container) error {
	cfg := appfacades.Config()
	challengeTTL := time.Duration(cfg.GetInt("auth.two_factor.challenge_ttl_seconds", defaultTwoFactorChallengeTTLSeconds)) * time.Second
	attemptWindow := time.Duration(cfg.GetInt("auth.two_factor.attempt_window_seconds", defaultTwoFactorAttemptWindowSeconds)) * time.Second
	maxAttempts := cfg.GetInt("auth.two_factor.max_attempts", defaultTwoFactorMaxAttempts)

	challenges, err := authsvc.NewCacheTOTPChallengeStore(authsvc.ChallengeStoreDeps{
		Cache: appfacades.Cache(),
		TTL:   challengeTTL,
	})
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	attempts, err := authsvc.NewCacheAttemptLimiter(authsvc.AttemptLimiterDeps{
		Cache:  appfacades.Cache(),
		Window: attemptWindow,
	})
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	bridge := authRepoBridge{users: c.UserRepo, recovery: c.TotpRecoveryCodeRepo, refresh: c.RefreshTokenRepo}
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  authsvc.NewService(),
		Counters: bridge,
		Recovery: bridge,
		Decrypt:  openSealedTotp,
	})
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	login, err := authsvc.NewTwoFactorLogin(authsvc.LoginDeps{
		Challenges:  challenges,
		Attempts:    attempts,
		Verifier:    verifier,
		Users:       bridge,
		MaxAttempts: maxAttempts,
	})
	if err != nil {
		return fmt.Errorf("vault: two factor login: %w", err)
	}
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: bridge,
		Refresh:    bridge,
		Activity:   repositories.NewAccountActivityRepository(nil),
	})
	if err != nil {
		return fmt.Errorf("vault: session revoker: %w", err)
	}

	c.SecondFactor = verifier
	c.TwoFactorLogin = login
	c.SessionRevoker = revoker
	return nil
}
