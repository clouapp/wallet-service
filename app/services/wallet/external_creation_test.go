package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/require"
)

type failedCreationAddresses struct {
	*memAddresses
	err error
}

func (f *failedCreationAddresses) Create(context.Context, *models.Address) error { return f.err }

type failedCreationLink struct {
	*memWallets
	err error
}

func (f *failedCreationLink) SetDepositAddressID(context.Context, uuid.UUID, uuid.UUID) error {
	return f.err
}

func TestCreateExternalWallet_ActiveWithoutActivationCredential(t *testing.T) {
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	wallets, addresses := newMemWallets(), newMemAddresses()
	service := newTestServiceWithRepos(t, registry, wallets, addresses)
	result, err := service.CreateExternalWallet(context.Background(), testAccountID, "eth", "headless", testPassphrase)
	require.NoError(t, err)
	require.Equal(t, "active", result.Wallet.Status)
	require.Nil(t, result.Wallet.ActivationCode)
	require.Empty(t, result.ActivationCode)
	require.NotNil(t, result.Wallet.DepositAddress)
	stored, err := wallets.FindByID(context.Background(), result.Wallet.ID)
	require.NoError(t, err)
	require.Equal(t, "active", stored.Status)
	require.Nil(t, stored.ActivationCode)
	require.Equal(t, &result.Wallet.DepositAddress.ID, stored.DepositAddressID)

	dashboard, err := service.CreateWallet(context.Background(), testAccountID, "eth", "dashboard", testPassphrase)
	require.NoError(t, err)
	require.Equal(t, "pending", dashboard.Wallet.Status)
	require.Regexp(t, `^[0-9]{6}$`, dashboard.ActivationCode)
}

func TestCreateExternalWallet_RollsBackWalletAndAddressWhenPersistenceFails(t *testing.T) {
	for _, phase := range []string{"address", "link"} {
		t.Run(phase, func(t *testing.T) {
			registry := chain.NewRegistry()
			registry.RegisterChain(mocks.NewMockChain("eth"))
			wallets, addresses := newMemWallets(), newMemAddresses()
			var walletStore WalletStore = wallets
			var addressStore AddressStore = addresses
			failure := errors.New("synthetic creation failure")
			if phase == "address" {
				addressStore = &failedCreationAddresses{addresses, failure}
			} else {
				walletStore = &failedCreationLink{wallets, failure}
			}
			service := newTestServiceWithRepos(t, registry, walletStore, addressStore)
			result, err := service.CreateExternalWallet(context.Background(), testAccountID, "eth", "headless", testPassphrase)
			require.ErrorIs(t, err, failure)
			require.Nil(t, result)
			require.Empty(t, wallets.rows)
			require.Empty(t, addresses.rows)
		})
	}
}

func TestCreateExternalWallet_RequiresAtomicLifecycleStore(t *testing.T) {
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	service := newTestService(t, registry)
	_, err := service.CreateExternalWallet(context.Background(), testAccountID, "eth", "headless", testPassphrase)
	require.ErrorIs(t, err, ErrLifecycleUnavailable)
}
