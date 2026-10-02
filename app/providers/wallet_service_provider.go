package providers

import (
	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/repositories"
)

// WalletServiceProvider binds the wallet and address repositories by type.
// wallet.Service stays constructed in the vault container: its registry, redis,
// MPC, and secrets dependencies are built there, and that constructor resolves
// these same repository instances.
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
}

func (p *WalletServiceProvider) Boot(foundation.Application) {}
