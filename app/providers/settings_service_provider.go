package providers

import (
	"github.com/goravel/framework/contracts/foundation"

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
		return settings.NewService(store, settings.CryptSealer{}, settings.FacadeCache{}, activityLog), nil
	})
}

func (p *SettingsServiceProvider) Boot(foundation.Application) {}
