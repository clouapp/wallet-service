package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
)

// ChainServiceProvider binds the chain, token, chain-resource, and currency
// repositories by type. Price and sweep stay constructed in the vault container
// and receive these same instances.
type ChainServiceProvider struct{}

func (p *ChainServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.ChainRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewChainRepository(nil), nil
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
}

func (p *ChainServiceProvider) Boot(foundation.Application) {}
