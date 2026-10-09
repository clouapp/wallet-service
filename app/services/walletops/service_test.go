package walletops_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletops"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type fakeWallets struct {
	created      *walletCall
	createResult *wallet.CreateWalletResult
	createErr    error
	activated    string
	activateErr  error

	generated   *models.Address
	generateErr error
	wallet      *models.Wallet
	walletErr   error

	updatedFields map[string]interface{}
	updatedID     uuid.UUID
	updateResult  *models.Address
	updateErr     error

	userAddressesFor string
	userAddresses    []models.Address
	userAddressesErr error

	lookups      []string
	lookupResult map[string]*models.Address
	lookupErr    error
}

type walletCall struct {
	accountID                uuid.UUID
	chain, label, passphrase string
}

func (f *fakeWallets) CreateWallet(_ context.Context, accountID uuid.UUID, chainID, label, passphrase string) (*wallet.CreateWalletResult, error) {
	f.created = &walletCall{accountID, chainID, label, passphrase}
	return f.createResult, f.createErr
}

func (f *fakeWallets) ActivateWallet(_ context.Context, walletID uuid.UUID, code string) (*models.Wallet, error) {
	f.activated = walletID.String() + "/" + code
	return nil, f.activateErr
}

func (f *fakeWallets) GetWallet(context.Context, uuid.UUID) (*models.Wallet, error) {
	return f.wallet, f.walletErr
}

func (f *fakeWallets) GenerateAddress(context.Context, uuid.UUID, string, string, string, string) (*models.Address, error) {
	return f.generated, f.generateErr
}

func (f *fakeWallets) UpdateAddress(_ context.Context, id uuid.UUID, fields map[string]interface{}) (*models.Address, error) {
	f.updatedID, f.updatedFields = id, fields
	return f.updateResult, f.updateErr
}

func (f *fakeWallets) LookupAddressForAccount(_ context.Context, chainID, _ string, _ uuid.UUID) (*models.Address, error) {
	f.lookups = append(f.lookups, chainID)
	return f.lookupResult[chainID], f.lookupErr
}

func (f *fakeWallets) ListUserAddressesForAccount(_ context.Context, externalUserID string, accountID uuid.UUID) ([]models.Address, error) {
	f.userAddressesFor = externalUserID + "/" + accountID.String()
	return f.userAddresses, f.userAddressesErr
}

type fakeCache struct {
	refreshed []string
	err       error
}

func (f *fakeCache) RefreshAddressCache(_ context.Context, chainID string) error {
	f.refreshed = append(f.refreshed, chainID)
	return f.err
}

type fakeRegistry []string

func (f fakeRegistry) ChainIDs() []string { return f }

type fakeChainCatalog struct {
	chainsvc.Catalog
	chain *models.Chain
	err   error
}

func (f fakeChainCatalog) FindByID(context.Context, string) (*models.Chain, error) {
	return f.chain, f.err
}

func newService(wallets *fakeWallets, cache *fakeCache, catalog fakeChainCatalog, registry fakeRegistry) *walletops.Service {
	return walletops.NewService(walletops.Deps{
		Wallets:   wallets,
		Addresses: &walletrecords.Addresses{},
		Chains:    chainsvc.NewService(chainsvc.Deps{Chains: catalog}),
		Cache:     cache,
		Registry:  registry,
	})
}

func TestNew_Service_RequiresEveryDependency(t *testing.T) {
	complete := walletops.Deps{
		Wallets: &fakeWallets{}, Addresses: &walletrecords.Addresses{}, Chains: &chainsvc.Service{},
		Cache: &fakeCache{}, Registry: fakeRegistry{},
	}
	cases := []struct {
		name  string
		clear func(*walletops.Deps)
		panic string
	}{
		{"wallet service", func(d *walletops.Deps) { d.Wallets = nil }, "wallet operations: wallet service is required"},
		{"addresses", func(d *walletops.Deps) { d.Addresses = nil }, "wallet operations: addresses service is required"},
		{"chains", func(d *walletops.Deps) { d.Chains = nil }, "wallet operations: chains service is required"},
		{"address cache", func(d *walletops.Deps) { d.Cache = nil }, "wallet operations: address cache is required"},
		{"registry", func(d *walletops.Deps) { d.Registry = nil }, "wallet operations: chain registry is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := complete
			tc.clear(&deps)
			defer func() { assert.Equal(t, tc.panic, recover()) }()
			walletops.NewService(deps)
			t.Fatal("expected a panic")
		})
	}
}

func TestCreateWalletInEnvironment(t *testing.T) {
	account := uuid.New()
	input := walletops.CreateWalletInput{AccountID: account, Environment: models.EnvironmentProd, Chain: "sepolia", Label: "hot", Passphrase: "a long passphrase"}
	created := &wallet.CreateWalletResult{ServicePublicKey: "pk"}

	t.Run("a chain of the other network kind is refused before any wallet is made", func(t *testing.T) {
		wallets := &fakeWallets{}
		svc := newService(wallets, &fakeCache{}, fakeChainCatalog{chain: &models.Chain{ID: "sepolia", IsTestnet: true}}, nil)

		_, err := svc.CreateWalletInEnvironment(context.Background(), input)

		require.ErrorIs(t, err, chainsvc.ErrChainNotInEnvironment)
		assert.Nil(t, wallets.created)
	})

	for name, catalog := range map[string]fakeChainCatalog{
		"a chain of the right kind":           {chain: &models.Chain{ID: "sepolia"}},
		"a chain the catalogue does not hold": {err: models.ErrRepositoryNotFound},
		"a catalogue that fails":              {err: errors.New("catalogue down")},
	} {
		t.Run(name+" is created by the wallet service", func(t *testing.T) {
			wallets := &fakeWallets{createResult: created}
			svc := newService(wallets, &fakeCache{}, catalog, nil)

			result, err := svc.CreateWalletInEnvironment(context.Background(), input)

			require.NoError(t, err)
			assert.Same(t, created, result)
			assert.Equal(t, &walletCall{account, "sepolia", "hot", "a long passphrase"}, wallets.created)
		})
	}

	t.Run("the wallet service failure is returned as it is", func(t *testing.T) {
		boom := errors.New("keygen failed")
		svc := newService(&fakeWallets{createErr: boom}, &fakeCache{}, fakeChainCatalog{chain: &models.Chain{ID: "sepolia"}}, nil)

		_, err := svc.CreateWalletInEnvironment(context.Background(), input)

		assert.ErrorIs(t, err, boom)
	})
}

func TestGenerateAddress(t *testing.T) {
	address := &models.Address{Address: "0xabc"}

	t.Run("reloads the address cache of the wallet's chain", func(t *testing.T) {
		cache := &fakeCache{}
		svc := newService(&fakeWallets{generated: address, wallet: &models.Wallet{Chain: "eth"}}, cache, fakeChainCatalog{}, nil)

		got, err := svc.GenerateAddress(context.Background(), walletops.GenerateAddressInput{WalletID: uuid.New()})

		require.NoError(t, err)
		assert.Same(t, address, got)
		assert.Equal(t, []string{"eth"}, cache.refreshed)
	})

	t.Run("a failed derivation reloads nothing", func(t *testing.T) {
		cache := &fakeCache{}
		boom := wallet.ErrInvalidPassphrase
		svc := newService(&fakeWallets{generateErr: boom, wallet: &models.Wallet{Chain: "eth"}}, cache, fakeChainCatalog{}, nil)

		_, err := svc.GenerateAddress(context.Background(), walletops.GenerateAddressInput{})

		require.ErrorIs(t, err, boom)
		assert.Empty(t, cache.refreshed)
	})

	t.Run("a wallet that cannot be reloaded or a cache that fails still returns the address", func(t *testing.T) {
		for name, svc := range map[string]*walletops.Service{
			"wallet read fails": newService(&fakeWallets{generated: address, walletErr: errors.New("down")}, &fakeCache{}, fakeChainCatalog{}, nil),
			"cache fails":       newService(&fakeWallets{generated: address, wallet: &models.Wallet{Chain: "eth"}}, &fakeCache{err: errors.New("redis down")}, fakeChainCatalog{}, nil),
		} {
			got, err := svc.GenerateAddress(context.Background(), walletops.GenerateAddressInput{})
			require.NoError(t, err, name)
			assert.Same(t, address, got, name)
		}
	})
}

func TestUpdateAddress(t *testing.T) {
	label, owner := "treasury", "user-9"

	t.Run("sends only the fields that are set", func(t *testing.T) {
		wallets := &fakeWallets{updateResult: &models.Address{}}
		svc := newService(wallets, &fakeCache{}, fakeChainCatalog{}, nil)
		id := uuid.New()

		_, err := svc.UpdateAddress(context.Background(), walletops.UpdateAddressInput{AddressID: id, Label: &label})
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"label": "treasury"}, wallets.updatedFields)
		assert.Equal(t, id, wallets.updatedID)

		_, err = svc.UpdateAddress(context.Background(), walletops.UpdateAddressInput{Label: &label, ExternalUserID: &owner})
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"label": "treasury", "external_user_id": "user-9"}, wallets.updatedFields)
	})

	t.Run("an empty string is a value, not an absent field", func(t *testing.T) {
		wallets := &fakeWallets{}
		empty := ""
		_, err := newService(wallets, &fakeCache{}, fakeChainCatalog{}, nil).UpdateAddress(context.Background(), walletops.UpdateAddressInput{Label: &empty})
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"label": ""}, wallets.updatedFields)
	})

	t.Run("an update that sets nothing is refused before the wallet service", func(t *testing.T) {
		wallets := &fakeWallets{}
		_, err := newService(wallets, &fakeCache{}, fakeChainCatalog{}, nil).UpdateAddress(context.Background(), walletops.UpdateAddressInput{})
		require.ErrorIs(t, err, walletops.ErrNoFields)
		assert.Nil(t, wallets.updatedFields)
	})
}

func TestLookupAddress(t *testing.T) {
	found := &models.Address{Address: "0xabc"}
	account := uuid.New()

	t.Run("with a chain it asks only that chain", func(t *testing.T) {
		wallets := &fakeWallets{lookupResult: map[string]*models.Address{"eth": found}}
		svc := newService(wallets, &fakeCache{}, fakeChainCatalog{}, fakeRegistry{"btc", "eth"})

		got, err := svc.LookupAddress(context.Background(), account, "0xabc", "eth")

		require.NoError(t, err)
		assert.Same(t, found, got)
		assert.Equal(t, []string{"eth"}, wallets.lookups)
	})

	t.Run("without a chain it tries each registered chain until one has it", func(t *testing.T) {
		wallets := &fakeWallets{lookupResult: map[string]*models.Address{"eth": found}}
		svc := newService(wallets, &fakeCache{}, fakeChainCatalog{}, fakeRegistry{"btc", "eth", "sol"})

		got, err := svc.LookupAddress(context.Background(), account, "0xabc", "")

		require.NoError(t, err)
		assert.Same(t, found, got)
		assert.Equal(t, []string{"btc", "eth"}, wallets.lookups)
	})

	t.Run("a miss on every chain, a lookup failure and a miss on the asked chain are not found", func(t *testing.T) {
		for name, tc := range map[string]struct {
			wallets *fakeWallets
			chain   string
		}{
			"no chain holds it":      {&fakeWallets{}, ""},
			"the asked chain fails":  {&fakeWallets{lookupErr: errors.New("down")}, "eth"},
			"the asked chain misses": {&fakeWallets{}, "eth"},
		} {
			_, err := newService(tc.wallets, &fakeCache{}, fakeChainCatalog{}, fakeRegistry{"btc"}).LookupAddress(context.Background(), account, "0xabc", tc.chain)
			assert.ErrorIs(t, err, walletops.ErrAddressNotFound, name)
		}
	})
}

type fakeAddressStore struct {
	walletrecords.AddressStore
	rows  []models.Address
	total int64
	err   error
	limit int
	skip  int
}

func (f *fakeAddressStore) PaginateByWalletID(_ context.Context, _ uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	f.limit, f.skip = limit, offset
	return f.rows, f.total, f.err
}

func TestListAddresses(t *testing.T) {
	deps := func(store *fakeAddressStore) walletops.Deps {
		return walletops.Deps{
			Wallets: &fakeWallets{}, Addresses: walletrecords.NewAddresses(store), Chains: &chainsvc.Service{},
			Cache: &fakeCache{}, Registry: fakeRegistry{},
		}
	}

	t.Run("returns the page with its total", func(t *testing.T) {
		store := &fakeAddressStore{rows: []models.Address{{Address: "0xabc"}}, total: 41}

		rows, total, err := walletops.NewService(deps(store)).ListAddresses(context.Background(), uuid.New(), 20, 40)

		require.NoError(t, err)
		assert.Equal(t, store.rows, rows)
		assert.Equal(t, int64(41), total)
		assert.Equal(t, [2]int{20, 40}, [2]int{store.limit, store.skip})
	})

	t.Run("a store failure is marked and keeps its cause", func(t *testing.T) {
		cause := errors.New("pq: connection refused")

		_, _, err := walletops.NewService(deps(&fakeAddressStore{err: cause})).ListAddresses(context.Background(), uuid.New(), 20, 0)

		require.ErrorIs(t, err, walletops.ErrAddressesUnavailable)
		require.ErrorIs(t, err, cause)
	})
}

func TestUserAddresses_AreScopedToTheAccount(t *testing.T) {
	account := uuid.New()
	rows := []models.Address{{Address: "0xabc"}}
	wallets := &fakeWallets{userAddresses: rows}
	svc := newService(wallets, &fakeCache{}, fakeChainCatalog{}, nil)

	got, err := svc.UserAddresses(context.Background(), account, "user_123")

	require.NoError(t, err)
	assert.Equal(t, rows, got)
	assert.Equal(t, "user_123/"+account.String(), wallets.userAddressesFor)

	boom := errors.New("store down")
	_, err = newService(&fakeWallets{userAddressesErr: boom}, &fakeCache{}, fakeChainCatalog{}, nil).UserAddresses(context.Background(), account, "user_123")
	assert.ErrorIs(t, err, boom)
}

func TestCreateWallet_HasNoEnvironmentCheck(t *testing.T) {
	account := uuid.New()
	created := &wallet.CreateWalletResult{ServicePublicKey: "pk"}
	wallets := &fakeWallets{createResult: created}
	svc := newService(wallets, &fakeCache{}, fakeChainCatalog{chain: &models.Chain{ID: "sepolia", IsTestnet: true}}, nil)

	result, err := svc.CreateWallet(context.Background(), account, "sepolia", "hot", "a long passphrase")

	require.NoError(t, err)
	assert.Same(t, created, result)
	assert.Equal(t, &walletCall{account, "sepolia", "hot", "a long passphrase"}, wallets.created)
}

func TestActivateWallet(t *testing.T) {
	id := uuid.New()
	wallets := &fakeWallets{}

	require.NoError(t, newService(wallets, &fakeCache{}, fakeChainCatalog{}, nil).ActivateWallet(context.Background(), id, "123456"))
	assert.Equal(t, id.String()+"/123456", wallets.activated)

	err := newService(&fakeWallets{activateErr: wallet.ErrInvalidActivationCode}, &fakeCache{}, fakeChainCatalog{}, nil).ActivateWallet(context.Background(), id, "000000")
	assert.ErrorIs(t, err, wallet.ErrInvalidActivationCode)
}
