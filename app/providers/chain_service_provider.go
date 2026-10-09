package providers

import (
	"fmt"
	"log/slog"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/settings"
)

// ChainServiceProvider binds the chain, token, chain-resource, and currency
// repositories by type, the chain catalogue services that read them, and the
// chain registry the catalogue fills.
type ChainServiceProvider struct{}

func (p *ChainServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.ChainRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewChainRepository(nil), nil
	})
	app.Singleton((*chainregistry.ChainRegistryService)(nil), func(app foundation.Application) (any, error) {
		chains, err := resolve[*repositories.ChainRepository](app)
		if err != nil {
			return nil, err
		}
		return chainregistry.NewChainRegistryService(chainregistry.ChainRegistryDeps{
			Store:    chainCatalog{repo: chains},
			Cache:    chainregistry.NewCatalogCache(),
			Install:  registerActiveChains,
			Registry: chainpkg.NewRegistry(),
		}), nil
	})
	app.Singleton((*chainpkg.Registry)(nil), func(app foundation.Application) (any, error) {
		return loadChainRegistry(app)
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

// loadChainRegistry installs the chain catalog Boot read, with the active
// tokens, on the registry every chain consumer shares. A catalog or token
// read that failed at Boot is logged and leaves those chains out; the process
// stays up.
func loadChainRegistry(app foundation.Application) (*chainpkg.Registry, error) {
	registryService, err := resolve[*chainregistry.ChainRegistryService](app)
	if err != nil {
		return nil, fmt.Errorf("vault: chain registry: %w", err)
	}
	activeTokens, loaded := bootedActiveTokens()
	if !loaded {
		slog.Error("failed to load tokens from DB", "error", errActiveTokensNotLoaded)
	}
	if err := registryService.Load(registerActiveTokens(nil, activeTokens)); err != nil {
		slog.Error("failed to load chains from DB", "error", err)
	}
	registry := registryService.Registry()
	slog.Info("chain registry loaded", "chains", registry.ChainIDs())
	return registry, nil
}

// chainRPCSealer seals chains.rpc_url with the process cipher. It writes the
// enc:v1: format and reads that and the bare Crypt envelope older rows hold.
// Errors do not include the URL.
type chainRPCSealer struct{}

func (chainRPCSealer) Seal(plaintext string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("seal chain rpc: crypt is not available")
	}
	sealed, err := settings.Seal(cipher, plaintext)
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
	opened, err := settings.OpenStored(cipher, stored)
	if err != nil || opened == "" {
		return "", fmt.Errorf("open chain rpc")
	}
	return opened, nil
}
