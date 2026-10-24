package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
)

// SettingsServiceProvider binds the account settings store and service.
type SettingsServiceProvider struct{}

func (p *SettingsServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.SettingRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewSettingRepository(nil), nil
	})
	app.Singleton((*settings.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.SettingRepository](app)
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
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		return settings.NewService(settings.Deps{Store: store, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityLog,
			Fallback: platformSettingsFallback(facades.Config()), IngestBaseDestinations: ingestBaseDestinations()}).
			WithPlatformAdmins(admins).
			WithAccounts(accounts).
			WithPlatformTestMailer(NewPlatformTestMailer()), nil
	})
}

func (p *SettingsServiceProvider) Boot(foundation.Application) {}

// platformSettingsFallback shows, on the platform views, the environment value of a
// platform field that takes it while no row stores it: the chains of
// deposit_signatures (DEPOSIT_SIGNATURE_CHAINS). A form that sends the whole group
// back then stores the chains in effect, and an env-listed chain is never turned off
// by a save that did not mean to change it; sending an empty list still does.
func platformSettingsFallback(cfg configReader) settings.PlatformFallback {
	return func(group, key string) (any, bool) {
		if group != settings.GroupDepositSignatures || key != settings.KeySignatureChains {
			return nil, false
		}
		return signatureSettingsFromEnv(cfg).Chains, true
	}
}
