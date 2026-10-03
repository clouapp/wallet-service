package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/currencies"
)

// ChainServiceProvider binds the chain, token, chain-resource, and currency
// repositories by type, and the chain catalogue service that reads them.
// Price and sweep stay constructed in the vault container and receive these
// same repository instances.
type ChainServiceProvider struct{}

func (p *ChainServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.ChainRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewChainRepository(nil), nil
	})
	app.Singleton((*chainsvc.Service)(nil), func(app foundation.Application) (any, error) {
		chains, err := resolve[*repositories.ChainRepository](app)
		if err != nil {
			return nil, err
		}
		return chainsvc.NewService(chains), nil
	})
	app.Singleton((*repositories.TokenRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewTokenRepository(nil), nil
	})
	app.Singleton((*repositories.ChainResourceRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewChainResourceRepository(nil), nil
	})
	app.Singleton((*repositories.CurrencyRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewCurrencyRepository(nil), nil
	})
	app.Singleton((*currencies.Service)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.CurrencyRepository](app)
		if err != nil {
			return nil, err
		}
		return currencies.NewService(store), nil
	})
}

func (p *ChainServiceProvider) Boot(foundation.Application) {}
