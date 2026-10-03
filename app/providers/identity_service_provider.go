package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
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
	app.Singleton((*account.Service)(nil), func(app foundation.Application) (any, error) {
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		memberships, err := resolve[*repositories.AccountUserRepository](app)
		if err != nil {
			return nil, err
		}
		return account.NewService(account.Deps{
			Accounts:    accounts,
			Memberships: memberships,
		}), nil
	})
}

func (p *IdentityServiceProvider) Boot(foundation.Application) {}
