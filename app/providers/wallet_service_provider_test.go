package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/stretchr/testify/assert"
)

func TestWallet_Provider_RegistersTheWalletGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&WalletServiceProvider{}).Register(foundation.App)

	wallets, err := container.Make[*repositories.WalletRepository]()
	require.NoError(t, err)
	require.NotNil(t, wallets)

	addresses, err := container.Make[*repositories.AddressRepository]()
	require.NoError(t, err)
	require.NotNil(t, addresses)

	members, err := container.Make[*repositories.WalletUserRepository]()
	require.NoError(t, err)
	assert.NotNil(t, members)

	whitelist, err := container.Make[*repositories.WhitelistEntryRepository]()
	require.NoError(t, err)
	assert.NotNil(t, whitelist)

	balances, err := container.Make[*repositories.WalletAssetBalanceRepository]()
	require.NoError(t, err)
	assert.NotNil(t, balances)

	snapshots, err := container.Make[*repositories.WalletBalanceSnapshotRepository]()
	require.NoError(t, err)
	assert.NotNil(t, snapshots)

	utxos, err := container.Make[*repositories.WalletUTXORepository]()
	require.NoError(t, err)
	assert.NotNil(t, utxos)

	syncState, err := container.Make[*repositories.WalletSyncStateRepository]()
	require.NoError(t, err)
	assert.NotNil(t, syncState)

	assert.Same(t, wallets, container.MustMake[*repositories.WalletRepository]())
	assert.Same(t, addresses, container.MustMake[*repositories.AddressRepository]())
}
