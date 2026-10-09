package walletview_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/walletview"
	"github.com/macrowallets/waas/pkg/numeric"
)

type fakeWalletStore struct {
	walletrecords.WalletStore
	page       []models.Wallet
	total      int64
	pageErr    error
	memberPage bool
	pagedUser  uuid.UUID
	chain      string
	owned      *models.Wallet
	ownedErr   error
}

func (f *fakeWalletStore) PaginateByAccount(_ context.Context, _ uuid.UUID, chain string, _, _ int) ([]models.Wallet, int64, error) {
	f.chain = chain
	return f.page, f.total, f.pageErr
}

func (f *fakeWalletStore) PaginateByAccountAndMember(_ context.Context, _, userID uuid.UUID, chain string, _, _ int) ([]models.Wallet, int64, error) {
	f.memberPage, f.pagedUser, f.chain = true, userID, chain
	return f.page, f.total, f.pageErr
}

func (f *fakeWalletStore) FindByIDAndAccount(context.Context, uuid.UUID, uuid.UUID) (*models.Wallet, error) {
	return f.owned, f.ownedErr
}

type fakeMemberStore struct {
	walletrecords.MemberStore
	err error
}

func (f fakeMemberStore) FindByWalletAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.WalletUser, error) {
	return &models.WalletUser{}, f.err
}

type fakeBalanceStore struct {
	walletrecords.BalanceStore
	rows    []models.WalletAssetBalance
	listErr error
}

func (f fakeBalanceStore) ListByWallet(context.Context, uuid.UUID) ([]models.WalletAssetBalance, error) {
	return f.rows, f.listErr
}

func (f fakeBalanceStore) ListByWallets(context.Context, []uuid.UUID) ([]models.WalletAssetBalance, error) {
	return f.rows, f.listErr
}

type fakeCatalog struct {
	chainsvc.Catalog
	records map[string]*models.Chain
	err     error
}

func (f fakeCatalog) FindByID(_ context.Context, id string) (*models.Chain, error) {
	return f.records[id], f.err
}

type fakeTokens struct {
	rows []models.Token
	err  error
}

func (f fakeTokens) FindByChainID(context.Context, string) ([]models.Token, error) {
	return f.rows, f.err
}

type fixture struct {
	wallets  *fakeWalletStore
	members  fakeMemberStore
	balances fakeBalanceStore
	catalog  fakeCatalog
	tokens   fakeTokens
	txs      *fakeTransactionStore
}

func (f fixture) service() *walletview.Service {
	return walletview.NewService(walletview.Deps{
		Wallets:      walletrecords.NewWallets(f.wallets),
		Members:      walletrecords.NewMembers(f.members),
		Balances:     walletrecords.NewBalances(f.balances),
		Transactions: walletrecords.NewTransactions(f.txs),
		Chains:       chainsvc.NewService(chainsvc.Deps{Chains: f.catalog, Tokens: f.tokens}),
		Cipher:       func() settings.Cipher { return nil },
	})
}

func newFixture() fixture {
	amoy := models.EVMNetworkIDPolygonAmoy
	return fixture{
		wallets: &fakeWalletStore{},
		txs:     &fakeTransactionStore{},
		catalog: fakeCatalog{records: map[string]*models.Chain{
			models.ChainPolygon: {ID: models.ChainPolygon, AdapterType: models.AdapterTypeEVM, NetworkID: &amoy},
			models.ChainETH:     {ID: models.ChainETH, AdapterType: models.AdapterTypeEVM},
		}},
	}
}

func usd(t *testing.T, text string) numeric.NullDecimal {
	t.Helper()
	value, err := decimal.NewFromString(text)
	require.NoError(t, err)
	return numeric.NewNullDecimal(value)
}

func TestNew_Service_RequiresEveryDependency(t *testing.T) {
	complete := walletview.Deps{
		Wallets: &walletrecords.Wallets{}, Members: &walletrecords.Members{}, Balances: &walletrecords.Balances{},
		Transactions: &walletrecords.Transactions{}, Chains: &chainsvc.Service{}, Cipher: func() settings.Cipher { return nil },
	}
	cases := []struct {
		name  string
		clear func(*walletview.Deps)
		panic string
	}{
		{"wallets", func(d *walletview.Deps) { d.Wallets = nil }, "wallet view: wallets service is required"},
		{"members", func(d *walletview.Deps) { d.Members = nil }, "wallet view: wallet members service is required"},
		{"balances", func(d *walletview.Deps) { d.Balances = nil }, "wallet view: balances service is required"},
		{"chains", func(d *walletview.Deps) { d.Chains = nil }, "wallet view: chains service is required"},
		{"cipher", func(d *walletview.Deps) { d.Cipher = nil }, "wallet view: cipher is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := complete
			tc.clear(&deps)
			defer func() { assert.Equal(t, tc.panic, recover()) }()
			walletview.NewService(deps)
			t.Fatal("expected a panic")
		})
	}
}

func TestList_ReturnsEveryWalletOfTheAccountForAnUnrestrictedViewer(t *testing.T) {
	f := newFixture()
	f.wallets.page, f.wallets.total = []models.Wallet{{ID: uuid.New(), Chain: models.ChainETH}}, 41

	page, err := f.service().List(context.Background(), walletview.ListInput{AccountID: uuid.New(), Chain: "eth", Limit: 20})

	require.NoError(t, err)
	assert.Equal(t, int64(41), page.Total)
	require.Len(t, page.Items, 1)
	assert.False(t, f.wallets.memberPage)
	assert.Equal(t, "eth", f.wallets.chain)
}

func TestList_LimitsAMemberOnlyViewerToTheirWallets(t *testing.T) {
	f := newFixture()
	user := uuid.New()

	_, err := f.service().List(context.Background(), walletview.ListInput{AccountID: uuid.New(), Viewer: walletview.Viewer{MemberOnly: true, UserID: user}})

	require.NoError(t, err)
	assert.True(t, f.wallets.memberPage)
	assert.Equal(t, user, f.wallets.pagedUser)
}

func TestList_RefusesACallerItCannotPlace(t *testing.T) {
	cases := map[string]struct {
		viewer walletview.Viewer
		want   error
	}{
		"no account":                          {walletview.Viewer{AccountMissing: true}, walletview.ErrAccountRequired},
		"a member-only caller without a user": {walletview.Viewer{MemberOnly: true}, walletview.ErrViewerRequired},
		"no account wins over no user":        {walletview.Viewer{AccountMissing: true, MemberOnly: true}, walletview.ErrAccountRequired},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture()

			_, err := f.service().List(context.Background(), walletview.ListInput{Viewer: tc.viewer})

			require.ErrorIs(t, err, tc.want)
			assert.Nil(t, f.wallets.page)
		})
	}
}

func TestList_MarksWhichReadFailed(t *testing.T) {
	t.Run("the page", func(t *testing.T) {
		f := newFixture()
		f.wallets.pageErr = errors.New("pq: down")

		_, err := f.service().List(context.Background(), walletview.ListInput{})

		var failed *walletview.FetchError
		require.ErrorAs(t, err, &failed)
		assert.Equal(t, "wallets", failed.What)
	})

	t.Run("the balances", func(t *testing.T) {
		f := newFixture()
		f.wallets.page = []models.Wallet{{ID: uuid.New(), Chain: models.ChainETH}}
		f.balances.listErr = errors.New("pq: down")

		_, err := f.service().List(context.Background(), walletview.ListInput{})

		var failed *walletview.FetchError
		require.ErrorAs(t, err, &failed)
		assert.Equal(t, "wallet balances", failed.What)
	})

	t.Run("the tokens of a chain", func(t *testing.T) {
		f := newFixture()
		f.wallets.page = []models.Wallet{{ID: uuid.New(), Chain: models.ChainETH}}
		f.tokens.err = errors.New("pq: down")

		_, err := f.service().List(context.Background(), walletview.ListInput{})

		var failed *walletview.FetchError
		require.ErrorAs(t, err, &failed)
		assert.Equal(t, "wallet balances", failed.What)
	})
}

func TestList_ItemCarriesTokenBalancesUnpricedOnATestnet(t *testing.T) {
	f := newFixture()
	walletID := uuid.New()
	usdc := "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	stored := []models.WalletAssetBalance{
		{WalletID: walletID, AssetType: "native", AssetSymbol: "POL", PriceUSD: usd(t, "0.5"), ValueUSD: usd(t, "2")},
		{WalletID: walletID, AssetType: "token", AssetSymbol: "USDC", AssetContract: &usdc, PriceUSD: usd(t, "1"), ValueUSD: usd(t, "6")},
	}
	f.balances.rows = stored
	f.tokens.rows = []models.Token{{ChainID: models.ChainPolygon, ContractAddress: usdc}}
	f.wallets.page = []models.Wallet{{ID: walletID, Chain: models.ChainPolygon, BalanceUSD: usd(t, "8")}}

	page, err := f.service().List(context.Background(), walletview.ListInput{})

	require.NoError(t, err)
	item := page.Items[0]
	assert.True(t, item.Network.Testnet)
	assert.False(t, item.Wallet.BalanceUSD.Valid, "a testnet wallet has no USD balance")
	require.Len(t, item.Assets, 2)
	for _, asset := range item.Assets {
		assert.False(t, asset.PriceUSD.Valid)
		assert.False(t, asset.ValueUSD.Valid)
	}
	assert.True(t, stored[1].ValueUSD.Valid, "the stored balance row is not touched")
}

func TestList_ItemKeepsTheUSDValuesOnAMainnet(t *testing.T) {
	f := newFixture()
	walletID := uuid.New()
	f.balances.rows = []models.WalletAssetBalance{{WalletID: walletID, AssetType: "native", AssetSymbol: "ETH", ValueUSD: usd(t, "9")}}
	f.wallets.page = []models.Wallet{{ID: walletID, Chain: models.ChainETH, BalanceUSD: usd(t, "9")}}

	page, err := f.service().List(context.Background(), walletview.ListInput{})

	require.NoError(t, err)
	assert.True(t, page.Items[0].Wallet.BalanceUSD.Valid)
	assert.True(t, page.Items[0].Assets[0].ValueUSD.Valid)
}

func TestList_ItemWithoutBalancesHasAnEmptyAssetList(t *testing.T) {
	f := newFixture()
	f.wallets.page = []models.Wallet{{ID: uuid.New(), Chain: models.ChainETH}}

	page, err := f.service().List(context.Background(), walletview.ListInput{})

	require.NoError(t, err)
	assert.NotNil(t, page.Items[0].Assets)
	assert.Empty(t, page.Items[0].Assets)
}

func TestGet(t *testing.T) {
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}
	in := func(viewer walletview.Viewer) walletview.GetInput {
		return walletview.GetInput{AccountID: uuid.New(), WalletID: wallet.ID, Viewer: viewer}
	}

	t.Run("returns the wallet with its network", func(t *testing.T) {
		f := newFixture()
		f.wallets.owned = wallet

		detail, err := f.service().Get(context.Background(), in(walletview.Viewer{}))

		require.NoError(t, err)
		assert.Same(t, wallet, detail.Wallet)
		assert.Equal(t, "polygon-amoy", detail.Network.Name)
	})

	t.Run("a wallet the account does not hold is not found, whatever the lookup says", func(t *testing.T) {
		for name, store := range map[string]*fakeWalletStore{
			"no row":         {},
			"missing":        {ownedErr: models.ErrRepositoryNotFound},
			"a store outage": {ownedErr: errors.New("pq: down")},
		} {
			f := newFixture()
			f.wallets = store

			_, err := f.service().Get(context.Background(), in(walletview.Viewer{}))

			assert.ErrorIs(t, err, walletview.ErrWalletNotFound, name)
		}
	})

	t.Run("a member-only caller needs the membership", func(t *testing.T) {
		member := walletview.Viewer{MemberOnly: true, UserID: uuid.New()}
		cases := map[string]struct {
			members fakeMemberStore
			viewer  walletview.Viewer
			want    error
		}{
			"member":              {fakeMemberStore{}, member, nil},
			"not a member":        {fakeMemberStore{err: models.ErrRepositoryNotFound}, member, walletview.ErrWalletNotFound},
			"unidentified":        {fakeMemberStore{}, walletview.Viewer{MemberOnly: true}, walletview.ErrViewerRequired},
			"account not carried": {fakeMemberStore{}, walletview.Viewer{AccountMissing: true}, walletview.ErrAccountRequired},
		}
		for name, tc := range cases {
			f := newFixture()
			f.wallets.owned = wallet
			f.members = tc.members

			_, err := f.service().Get(context.Background(), in(tc.viewer))

			if tc.want == nil {
				assert.NoError(t, err, name)
			} else {
				assert.ErrorIs(t, err, tc.want, name)
			}
		}
	})

	t.Run("a failed membership read is a fetch failure, not a hidden wallet", func(t *testing.T) {
		f := newFixture()
		f.wallets.owned = wallet
		f.members = fakeMemberStore{err: errors.New("pq: down")}

		_, err := f.service().Get(context.Background(), in(walletview.Viewer{MemberOnly: true, UserID: uuid.New()}))

		var failed *walletview.FetchError
		require.ErrorAs(t, err, &failed)
		assert.Equal(t, "wallet", failed.What)
	})
}

func TestBalances(t *testing.T) {
	usdc := "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}

	t.Run("keeps the configured assets and no USD value on a testnet", func(t *testing.T) {
		f := newFixture()
		f.balances.rows = []models.WalletAssetBalance{
			{AssetType: "native", AssetSymbol: "POL", ValueUSD: usd(t, "2")},
			{AssetType: "token", AssetSymbol: "USDC", AssetContract: &usdc, ValueUSD: usd(t, "6")},
			{AssetType: "token", AssetSymbol: "GHOST"},
		}
		f.tokens.rows = []models.Token{{ContractAddress: usdc}}

		got, err := f.service().Balances(context.Background(), wallet)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.False(t, got[0].ValueUSD.Valid)
		assert.False(t, got[1].ValueUSD.Valid)
	})

	t.Run("marks which read failed", func(t *testing.T) {
		balanceFailure := newFixture()
		balanceFailure.balances.listErr = errors.New("pq: down")
		tokenFailure := newFixture()
		tokenFailure.tokens.err = errors.New("pq: down")

		for what, f := range map[string]fixture{"balances": balanceFailure, "chain tokens": tokenFailure} {
			_, err := f.service().Balances(context.Background(), wallet)

			var failed *walletview.FetchError
			require.ErrorAs(t, err, &failed, what)
			assert.Equal(t, what, failed.What)
		}
	})
}

func TestNetwork_LeavesTheNetworkUnknownWhenTheChainCannotBeRead(t *testing.T) {
	for name, catalog := range map[string]fakeCatalog{
		"unknown chain": {},
		"missing row":   {err: models.ErrRepositoryNotFound},
		"store outage":  {err: errors.New("pq: down")},
	} {
		f := newFixture()
		f.catalog = catalog

		assert.Equal(t, models.ResolvedNetwork{}, f.service().Network(context.Background(), "nope"), name)
	}
}
