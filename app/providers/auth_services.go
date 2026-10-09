package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
	"github.com/macrowallets/waas/app/services/settings"
	usersvc "github.com/macrowallets/waas/app/services/users"
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

// newSecondFactorVerifier checks a TOTP code or a recovery code against the
// sealed secret. Login, withdrawals and the profile routes share it.
func newSecondFactorVerifier(app foundation.Application) (*authsvc.SecondFactorVerifier, error) {
	bridge, err := newAuthRepoBridge(app)
	if err != nil {
		return nil, err
	}
	passwords, err := resolve[*authsvc.Service](app)
	if err != nil {
		return nil, err
	}
	verifier, err := authsvc.NewSecondFactorVerifier(authsvc.VerifierDeps{
		Service:  passwords,
		Counters: bridge,
		Recovery: bridge,
		Decrypt:  openSealedTotp,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: two factor login: %w", err)
	}
	return verifier, nil
}

// newTwoFactorLogin keeps the login challenge and the attempt counter in the
// cache, with the windows from auth.two_factor.
func newTwoFactorLogin(app foundation.Application) (*authsvc.TwoFactorLogin, error) {
	cfg := appfacades.Config()
	challengeTTL := time.Duration(cfg.GetInt("auth.two_factor.challenge_ttl_seconds", defaultTwoFactorChallengeTTLSeconds)) * time.Second
	attemptWindow := time.Duration(cfg.GetInt("auth.two_factor.attempt_window_seconds", defaultTwoFactorAttemptWindowSeconds)) * time.Second
	maxAttempts := cfg.GetInt("auth.two_factor.max_attempts", defaultTwoFactorMaxAttempts)

	challenges, err := authsvc.NewCacheTOTPChallengeStore(authsvc.ChallengeStoreDeps{
		Cache: appfacades.Cache(),
		TTL:   challengeTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: two factor login: %w", err)
	}
	attempts, err := authsvc.NewCacheAttemptLimiter(authsvc.AttemptLimiterDeps{
		Cache:  appfacades.Cache(),
		Window: attemptWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: two factor login: %w", err)
	}
	verifier, err := resolve[*authsvc.SecondFactorVerifier](app)
	if err != nil {
		return nil, err
	}
	bridge, err := newAuthRepoBridge(app)
	if err != nil {
		return nil, err
	}
	login, err := authsvc.NewTwoFactorLogin(authsvc.LoginDeps{
		Challenges:  challenges,
		Attempts:    attempts,
		Verifier:    verifier,
		Users:       bridge,
		MaxAttempts: maxAttempts,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: two factor login: %w", err)
	}
	return login, nil
}

// newSessionRevoker moves a user's session watermark and revokes the refresh
// tokens, recording the revocation in the account activity.
func newSessionRevoker(app foundation.Application) (*authsvc.SessionRevoker, error) {
	bridge, err := newAuthRepoBridge(app)
	if err != nil {
		return nil, err
	}
	activityLog, err := resolve[*repositories.AccountActivityRepository](app)
	if err != nil {
		return nil, err
	}
	revoker, err := authsvc.NewSessionRevoker(authsvc.RevokerDeps{
		Watermarks: bridge,
		Refresh:    bridge,
		Activity:   activityLog,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: session revoker: %w", err)
	}
	return revoker, nil
}

func newAuthRepoBridge(app foundation.Application) (authRepoBridge, error) {
	users, err := resolve[*repositories.UserRepository](app)
	if err != nil {
		return authRepoBridge{}, err
	}
	recovery, err := resolve[*repositories.TotpRecoveryCodeRepository](app)
	if err != nil {
		return authRepoBridge{}, err
	}
	refresh, err := resolve[*repositories.RefreshTokenRepository](app)
	if err != nil {
		return authRepoBridge{}, err
	}
	return authRepoBridge{users: users, recovery: recovery, refresh: refresh}, nil
}

// newSessionIssuer mints the dashboard sessions with a stored refresh token,
// after the revocation watermark the revoker keeps.
func newSessionIssuer(app foundation.Application) (*authsvc.SessionIssuer, error) {
	passwords, err := resolve[*authsvc.Service](app)
	if err != nil {
		return nil, err
	}
	refresh, err := resolve[*sessions.RefreshTokens](app)
	if err != nil {
		return nil, err
	}
	revoker, err := resolve[*authsvc.SessionRevoker](app)
	if err != nil {
		return nil, err
	}
	issuer, err := authsvc.NewSessionIssuer(authsvc.IssuerDeps{
		Passwords: passwords,
		Refresh:   refresh,
		Revoker:   revoker,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: session issuer: %w", err)
	}
	return issuer, nil
}

// newCredentials changes a password, signed in or with a reset token, and ends
// the sessions the old one opened.
func newCredentials(app foundation.Application) (*authsvc.Credentials, error) {
	passwords, err := resolve[*authsvc.Service](app)
	if err != nil {
		return nil, err
	}
	users, err := resolve[*usersvc.Service](app)
	if err != nil {
		return nil, err
	}
	resets, err := resolve[*sessions.PasswordResets](app)
	if err != nil {
		return nil, err
	}
	issuer, err := resolve[*authsvc.SessionIssuer](app)
	if err != nil {
		return nil, err
	}
	revoker, err := resolve[*authsvc.SessionRevoker](app)
	if err != nil {
		return nil, err
	}
	credentials, err := authsvc.NewCredentials(authsvc.CredentialsDeps{
		Passwords: passwords,
		Users:     users,
		Resets:    resets,
		Sessions:  issuer,
		Revoker:   revoker,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: credentials: %w", err)
	}
	return credentials, nil
}
