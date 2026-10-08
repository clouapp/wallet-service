package providers

import (
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/adapters/redis/addresscache"
	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhooksync"
)

// WalletServiceProvider binds the wallet and address repositories by type,
// the wallet record readers over them, the wallet service, and the balance
// read model with the refresher that keeps it current.
type WalletServiceProvider struct{}

func (p *WalletServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.WalletRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletRepository(nil), nil
	})
	app.Singleton((*repositories.AddressRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewAddressRepository(nil), nil
	})
	app.Singleton((*repositories.WalletUserRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletUserRepository(nil), nil
	})
	app.Singleton((*repositories.WhitelistEntryRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWhitelistEntryRepository(nil), nil
	})
	app.Singleton((*repositories.WalletAssetBalanceRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletAssetBalanceRepository(nil), nil
	})
	app.Singleton((*repositories.WalletBalanceSnapshotRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletBalanceSnapshotRepository(nil), nil
	})
	app.Singleton((*repositories.WalletUTXORepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletUTXORepository(nil), nil
	})
	app.Singleton((*repositories.WalletSyncStateRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWalletSyncStateRepository(nil), nil
	})
	app.Singleton((*walletrecords.Wallets)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WalletRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewWallets(store), nil
	})
	app.Singleton((*walletrecords.Balances)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WalletAssetBalanceRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewBalances(store), nil
	})
	app.Singleton((*walletrecords.UTXOs)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WalletUTXORepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewUTXOs(store), nil
	})
	app.Singleton((*walletrecords.Members)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WalletUserRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewMembers(store), nil
	})
	app.Singleton((*walletrecords.Whitelist)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WhitelistEntryRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewWhitelist(store), nil
	})
	app.Singleton((*walletrecords.Addresses)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.AddressRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewAddresses(store), nil
	})
	app.Singleton((*wallet.Service)(nil), func(app foundation.Application) (any, error) {
		return newWalletService(app)
	})
	app.Singleton((*refresh.BalanceService)(nil), func(app foundation.Application) (any, error) {
		return newBalanceService(app)
	})
	app.Singleton((*refresh.WalletRefresher)(nil), func(app foundation.Application) (any, error) {
		return newWalletRefresher(app)
	})
}

func (p *WalletServiceProvider) Boot(foundation.Application) {}

// newWalletService creates wallets and derives their addresses: MPC keygen,
// share B in Secrets Manager, the Redis address cache and the provider
// subscriptions.
func newWalletService(app foundation.Application) (*wallet.Service, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	mpcService, err := resolve[*mpc.TSSService](app)
	if err != nil {
		return nil, err
	}
	secrets, err := resolve[*sweepsecrets.SDKClient](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	webhookSync, err := resolve[*webhooksync.Service](app)
	if err != nil {
		return nil, err
	}
	redisClient, err := facades.Redis()
	if err != nil {
		return nil, fmt.Errorf("vault: redis: %w", err)
	}
	return wallet.NewService(wallet.Deps{
		Registry:     registry,
		AddressCache: addresscache.New(redisClient),
		MPC:          mpcService,
		Secrets:      sweepsecrets.NewWalletStore(secrets),
		Wallets:      wallets,
		Addresses:    addresses,
		WebhookSync:  webhookSync,
	}), nil
}

// newBalanceService refreshes the wallet balance read model from the chains.
func newBalanceService(app foundation.Application) (*refresh.BalanceService, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	assetBalances, err := resolve[*repositories.WalletAssetBalanceRepository](app)
	if err != nil {
		return nil, err
	}
	snapshots, err := resolve[*repositories.WalletBalanceSnapshotRepository](app)
	if err != nil {
		return nil, err
	}
	syncStates, err := resolve[*repositories.WalletSyncStateRepository](app)
	if err != nil {
		return nil, err
	}
	return refresh.NewBalanceService(refresh.Deps{
		Registry:      registry,
		Wallets:       wallets,
		AssetBalances: assetBalances,
		Snapshots:     snapshots,
		SyncStates:    syncStates,
	}), nil
}

// newWalletRefresher walks every wallet through the balance service, spaced
// by vault.local_workers.balance_refresh_spacing_ms. The local worker loop and
// the deposit service share it.
func newWalletRefresher(app foundation.Application) (*refresh.WalletRefresher, error) {
	balances, err := resolve[*refresh.BalanceService](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	refresher, err := refresh.NewWalletRefresher(refresh.WalletRefresherDeps{
		Balances: balances,
		Wallets:  wallets,
		Chains:   registry,
		Spacing:  time.Duration(facades.Config().GetInt("vault.local_workers.balance_refresh_spacing_ms")) * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: wallet refresher: %w", err)
	}
	return refresher, nil
}
