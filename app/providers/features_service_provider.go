package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/features"
)

// FeaturesServiceProvider binds the account feature-flag store and service.
type FeaturesServiceProvider struct{}

func (p *FeaturesServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.FeatureRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewFeatureRepository(nil), nil
	})
	app.Singleton((*features.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.FeatureRepository](app)
		if err != nil {
			return nil, err
		}
		return features.NewService(store), nil
	})
}

func (p *FeaturesServiceProvider) Boot(foundation.Application) {}
