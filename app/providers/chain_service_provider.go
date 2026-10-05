package providers

import (
	"fmt"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/pkg/security"
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
		tokens, err := resolve[*repositories.TokenRepository](app)
		if err != nil {
			return nil, err
		}
		resources, err := resolve[*repositories.ChainResourceRepository](app)
		if err != nil {
			return nil, err
		}
		return chainsvc.NewService(chainsvc.Deps{
			Chains:    chains,
			Tokens:    tokens,
			Resources: resources,
		}), nil
	})
	app.Singleton((*chainsvc.Thresholds)(nil), func(app foundation.Application) (any, error) {
		chains, err := resolve[*repositories.ChainRepository](app)
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
		return chainsvc.NewThresholds(chainsvc.ThresholdDeps{
			Store:    chains,
			Admins:   admins,
			Activity: activityLog,
		}), nil
	})
	app.Singleton((*chainsvc.RPC)(nil), func(app foundation.Application) (any, error) {
		chains, err := resolve[*repositories.ChainRepository](app)
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
		registry, err := resolve[*chainpkg.Registry](app)
		if err != nil {
			return nil, err
		}
		return chainsvc.NewRPC(chainsvc.RPCDeps{
			Store:    chains,
			Admins:   admins,
			Activity: activityLog,
			Sealer:   chainRPCSealer{},
			Dialer:   registry,
		}), nil
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
		return currencies.NewService(currencies.Deps{Store: store}), nil
	})
}

func (p *ChainServiceProvider) Boot(foundation.Application) {}

// chainRPCSealer seals chains.rpc_url with the process cipher. Errors do not
// include the URL.
type chainRPCSealer struct{}

func (chainRPCSealer) Seal(plaintext string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("seal chain rpc: crypt is not available")
	}
	sealed, err := security.SealSecret(cipher, plaintext)
	if err != nil || sealed == "" || sealed == plaintext {
		return "", fmt.Errorf("seal chain rpc")
	}
	return sealed, nil
}

func (chainRPCSealer) Open(stored string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("open chain rpc: crypt is not available")
	}
	opened, err := security.OpenSecret(cipher, stored)
	if err != nil || opened == "" {
		return "", fmt.Errorf("open chain rpc")
	}
	return opened, nil
}
