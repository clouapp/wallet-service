package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/features"
)

// FeaturesServiceProvider binds the feature-flag store, the platform-admin
// lookup, and the service that reads both.
type FeaturesServiceProvider struct{}

func (p *FeaturesServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.FeatureRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewFeatureRepository(nil), nil
	})
	app.Singleton((*repositories.PlatformAdminRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewPlatformAdminRepository(nil), nil
	})
	app.Singleton((*features.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.FeatureRepository](app)
		if err != nil {
			return nil, err
		}
		admins, err := resolve[*repositories.PlatformAdminRepository](app)
		if err != nil {
			return nil, err
		}
		activityLog, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		return features.NewService(features.Deps{
			Store:    store,
			Admins:   admins,
			Activity: activityLog,
		}), nil
	})
}

func (p *FeaturesServiceProvider) Boot(foundation.Application) {}
