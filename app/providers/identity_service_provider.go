package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/listeners"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// IdentityServiceProvider binds the account-and-user repositories and the
// account service by type. The vault container still holds the same instances
// for callers that have not moved off container.Get.
type IdentityServiceProvider struct{}

func (p *IdentityServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.UserRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewUserRepository(nil), nil
	})
	app.Singleton((*repositories.AccountRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAccountRepository(nil), nil
	})
	app.Singleton((*repositories.AccountUserRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAccountUserRepository(nil), nil
	})
	app.Singleton((*repositories.AccessTokenRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAccessTokenRepository(nil), nil
	})
	app.Singleton((*repositories.AccountInviteRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAccountInviteRepository(nil), nil
	})
	app.Singleton((*repositories.RefreshTokenRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewRefreshTokenRepository(nil), nil
	})
	app.Singleton((*repositories.PasswordResetTokenRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewPasswordResetTokenRepository(nil), nil
	})
	app.Singleton((*repositories.TotpRecoveryCodeRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewTotpRecoveryCodeRepository(nil), nil
	})
	app.Singleton((*authsvc.Service)(nil), func(foundation.Application) (any, error) {
		return authsvc.NewService(), nil
	})
	app.Singleton((*usersvc.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.UserRepository](app)
		if err != nil {
			return nil, err
		}
		recovery, err := resolve[*repositories.TotpRecoveryCodeRepository](app)
		if err != nil {
			return nil, err
		}
		activityLog, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		admins, err := resolve[*repositories.PlatformAdminRepository](app)
		if err != nil {
			return nil, err
		}
		revoker, err := resolve[*authsvc.SessionRevoker](app)
		if err != nil {
			return nil, err
		}
		return usersvc.NewService(usersvc.Deps{
			Store:     store,
			Recovery:  recovery,
			Activity:  activityLog,
			Admins:    admins,
			Sessions:  revoker,
			ResetMail: listeners.NewCredentialMailDispatcher(),
		}), nil
	})
	app.Singleton((*sessions.RefreshTokens)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.RefreshTokenRepository](app)
		if err != nil {
			return nil, err
		}
		return sessions.NewRefreshTokens(store), nil
	})
	app.Singleton((*sessions.PasswordResets)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.PasswordResetTokenRepository](app)
		if err != nil {
			return nil, err
		}
		return sessions.NewPasswordResets(store), nil
	})
	app.Singleton((*account.Service)(nil), func(app foundation.Application) (any, error) {
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		memberships, err := resolve[*repositories.AccountUserRepository](app)
		if err != nil {
			return nil, err
		}
		users, err := resolve[*repositories.UserRepository](app)
		if err != nil {
			return nil, err
		}
		tokens, err := resolve[*repositories.AccessTokenRepository](app)
		if err != nil {
			return nil, err
		}
		activityLog, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		invites, err := resolve[*repositories.AccountInviteRepository](app)
		if err != nil {
			return nil, err
		}
		admins, err := resolve[*repositories.PlatformAdminRepository](app)
		if err != nil {
			return nil, err
		}
		return account.NewService(account.Deps{
			Accounts:    accounts,
			Memberships: memberships,
			Users:       users,
			Tokens:      tokens,
			Activity:    activityLog,
			Invites:     invites,
			InviteMail:  listeners.NewCredentialMailDispatcher(),
		}).WithPlatformAdmins(admins), nil
	})
}

func (p *IdentityServiceProvider) Boot(foundation.Application) {}
