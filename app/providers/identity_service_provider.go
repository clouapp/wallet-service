package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// IdentityServiceProvider binds the account-and-user repositories, the
// account and user services, and the second-factor and session services by
// type.
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
		return authsvc.NewService(appfacades.Hash()), nil
	})
	app.Singleton((*authsvc.SecondFactorVerifier)(nil), func(app foundation.Application) (any, error) {
		return newSecondFactorVerifier(app)
	})
	app.Singleton((*authsvc.TwoFactorLogin)(nil), func(app foundation.Application) (any, error) {
		return newTwoFactorLogin(app)
	})
	app.Singleton((*authsvc.SessionRevoker)(nil), func(app foundation.Application) (any, error) {
		return newSessionRevoker(app)
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
			ResetMail: newCredentialMailDispatcher(app),
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
		passwords, err := resolve[*authsvc.Service](app)
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
			InviteMail:  newCredentialMailDispatcher(app),
			Passwords:   passwords,
		}).WithPlatformAdmins(admins), nil
	})
}

func (p *IdentityServiceProvider) Boot(foundation.Application) {}
