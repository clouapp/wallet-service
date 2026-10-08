package chainregistry

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
)

const (
	sepoliaRPC     = "https://ethereum-sepolia-rpc.publicnode.com"
	amoyRPC        = "https://polygon-amoy-bor-rpc.publicnode.com"
	btcMainnetRPC  = "https://blockstream.info/api"
	btcTestnetRPC  = "https://blockstream.info/testnet/api"
	solanaDevnet   = "https://api.devnet.solana.com"
	solanaMainnet  = "https://api.mainnet-beta.solana.com"
	baseSepoliaRPC = "https://base-sepolia-rpc.publicnode.com"
	arbSepoliaRPC  = "https://arbitrum-sepolia-rpc.publicnode.com"
	bscTestnetRPC  = "https://bsc-testnet-rpc.publicnode.com"
	tronNileRPC    = "https://nile.trongrid.io"
	ltcTestnetRPC  = "https://litecoinspace.org/testnet/api"
	compressedPub  = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	mainnetGenesis = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
)

type fakeStore struct {
	chains     map[string]models.Chain
	funded     map[string]bool
	accounts   map[uuid.UUID]models.Account
	wallets    map[string][]models.Wallet
	addresses  map[uuid.UUID][]models.Address
	chainWrite int
}

func (f *fakeStore) Chains(context.Context) ([]models.Chain, error) {
	out := make([]models.Chain, 0, len(f.chains))
	for _, id := range append(append([]string{}, models.PrimaryChainIDs...), models.ChainTPolygon) {
		if ch, ok := f.chains[id]; ok {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (f *fakeStore) ChainHoldsBalance(_ context.Context, chainID string) (bool, error) {
	return f.funded[chainID], nil
}

func (f *fakeStore) UpdateChainNetwork(_ context.Context, chainID string, networkID *int64, isTestnet bool) error {
	ch := f.chains[chainID]
	ch.NetworkID, ch.IsTestnet = networkID, isTestnet
	f.chains[chainID] = ch
	f.chainWrite++
	return nil
}

func (f *fakeStore) FindAccount(_ context.Context, id uuid.UUID) (*models.Account, error) {
	account, ok := f.accounts[id]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return &account, nil
}

func (f *fakeStore) UpdateAccountEnvironment(_ context.Context, id uuid.UUID, environment string) error {
	account := f.accounts[id]
	account.Environment = environment
	f.accounts[id] = account
	return nil
}

func (f *fakeStore) WalletsOnChain(_ context.Context, chainID string) ([]models.Wallet, error) {
	return f.wallets[chainID], nil
}

func (f *fakeStore) ActiveAddressesOfWallet(_ context.Context, walletID uuid.UUID) ([]models.Address, error) {
	var active []models.Address
	for _, a := range f.addresses[walletID] {
		if a.IsActive {
			active = append(active, a)
		}
	}
	return active, nil
}

func (f *fakeStore) ReissueGenesis(_ context.Context, walletID uuid.UUID, genesis models.Address, retire []uuid.UUID) error {
	retired := make(map[uuid.UUID]bool, len(retire))
	for _, id := range retire {
		retired[id] = true
	}
	rows := f.addresses[walletID]
	for i := range rows {
		if retired[rows[i].ID] {
			rows[i].IsActive = false
		}
	}
	if genesis.Address != "" {
		rows = append(rows, genesis)
	}
	f.addresses[walletID] = rows
	return nil
}

func plainDecrypt(s string) (string, error) { return s, nil }

func ptr(v int64) *int64 { return &v }

// vaultTestRegistry is the vault_test registry before the fix: eth signs for
// mainnet over a Sepolia RPC, btc is mainnet, polygon is Amoy but listed as
// mainnet, sol runs on devnet but is listed as mainnet. base, arbitrum, bsc, tron
// and ltc were created by chains:add-missing on their testnets.
func vaultTestRegistry() *fakeStore {
	return &fakeStore{
		chains: map[string]models.Chain{
			models.ChainETH:      {ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDEthereumMainnet), RpcURL: sepoliaRPC},
			models.ChainBTC:      {ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, RpcURL: btcTestnetRPC},
			models.ChainPolygon:  {ID: models.ChainPolygon, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDPolygonAmoy), RpcURL: amoyRPC},
			models.ChainSOL:      {ID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, RpcURL: solanaDevnet},
			models.ChainTPolygon: {ID: models.ChainTPolygon, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDPolygonAmoy), IsTestnet: true, RpcURL: amoyRPC},
			models.ChainBase:     {ID: models.ChainBase, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDBaseSepolia), IsTestnet: true, RpcURL: baseSepoliaRPC},
			models.ChainArbitrum: {ID: models.ChainArbitrum, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDArbitrumSepolia), IsTestnet: true, RpcURL: arbSepoliaRPC},
			models.ChainBSC:      {ID: models.ChainBSC, AdapterType: models.AdapterTypeEVM, NetworkID: ptr(models.EVMNetworkIDBSCTestnet), IsTestnet: true, RpcURL: bscTestnetRPC},
			models.ChainTron:     {ID: models.ChainTron, AdapterType: models.AdapterTypeTron, IsTestnet: true, RpcURL: tronNileRPC},
			models.ChainLTC:      {ID: models.ChainLTC, AdapterType: models.AdapterTypeBitcoin, IsTestnet: true, RpcURL: ltcTestnetRPC},
		},
		funded:    map[string]bool{models.ChainPolygon: true},
		accounts:  map[uuid.UUID]models.Account{},
		wallets:   map[string][]models.Wallet{},
		addresses: map[uuid.UUID][]models.Address{},
	}
}

func changeByChain(plan *Plan) map[string]ChainChange {
	out := make(map[string]ChainChange, len(plan.Changes))
	for _, c := range plan.Changes {
		out[c.ChainID] = c
	}
	return out
}

func TestTestnet_Plan_FlipsEveryPrimaryChainButKeepsFundedPolygonOnAmoy(t *testing.T) {
	store := vaultTestRegistry()

	plan, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, nil)
	require.NoError(t, err)

	changes := changeByChain(plan)
	require.Len(t, changes, 4)
	assert.Equal(t, models.NetworkEthereumMainnet, changes[models.ChainETH].FromNetwork)
	assert.Equal(t, models.NetworkEthereumSepolia, changes[models.ChainETH].ToNetwork)
	assert.Equal(t, models.EVMNetworkIDEthereumSepolia, *changes[models.ChainETH].ToNetworkID)
	assert.Equal(t, models.NetworkBitcoinTestnet, changes[models.ChainBTC].ToNetwork)

	polygon := changes[models.ChainPolygon]
	assert.False(t, polygon.NetworkChanges(), "polygon only changes environment list")
	assert.Equal(t, models.EVMNetworkIDPolygonAmoy, *polygon.ToNetworkID)
	assert.True(t, polygon.ToTestnet)

	sol := changes[models.ChainSOL]
	assert.False(t, sol.NetworkChanges(), "sol already runs on devnet")
	assert.True(t, sol.ToTestnet)
	assert.NotContains(t, changes, models.ChainTPolygon)
}

func TestApplied_Plan_IsIdempotent(t *testing.T) {
	store := vaultTestRegistry()
	ctx := context.Background()

	plan, err := BuildPlan(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil)
	require.NoError(t, err)
	require.NoError(t, ApplyPlan(ctx, store, plan))
	writes := store.chainWrite

	again, err := BuildPlan(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil)
	require.NoError(t, err)
	assert.Empty(t, again.Changes)
	require.NoError(t, ApplyPlan(ctx, store, again))
	assert.Equal(t, writes, store.chainWrite)
}

func TestPlan_Refuses_ToMoveAFundedChainToAnotherNetwork(t *testing.T) {
	store := vaultTestRegistry()

	_, err := BuildPlan(context.Background(), models.ChainNetworkProfileMainnet, store, plainDecrypt, nil)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFundedChainNetworkChange), err)
	assert.Contains(t, err.Error(), models.ChainPolygon)
	assert.Zero(t, store.chainWrite)
}

func TestPlan_Refuses_WhenTheProbeSeesAnotherNetwork(t *testing.T) {
	store := vaultTestRegistry()
	btc := store.chains[models.ChainBTC]
	btc.RpcURL = btcMainnetRPC
	store.chains[models.ChainBTC] = btc

	_, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, staticProbe)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCNetworkMismatch), err)
	assert.Contains(t, err.Error(), "chains:set-rpc")
}

func TestTestnet_Plan_AcceptsBitcoinOnTestnet4(t *testing.T) {
	store := vaultTestRegistry()
	btc := store.chains[models.ChainBTC]
	btc.RpcURL = "https://mempool.space/testnet4/api"
	store.chains[models.ChainBTC] = btc

	plan, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, staticProbe)
	require.NoError(t, err)

	change := changeByChain(plan)[models.ChainBTC]
	assert.Equal(t, models.NetworkBitcoinMainnet, change.FromNetwork)
	assert.Equal(t, models.NetworkBitcoinTestnet4, change.ToNetwork)
	assert.True(t, change.ToTestnet)
	assert.Empty(t, plan.Warnings)
}

func TestMainnet_Plan_RefusesBitcoinOnTestnet4(t *testing.T) {
	store := vaultTestRegistry()
	delete(store.funded, models.ChainPolygon)
	btc := store.chains[models.ChainBTC]
	btc.RpcURL = "https://mempool.space/testnet4/api"
	store.chains[models.ChainBTC] = btc

	bitcoinOnly := func(_ context.Context, record models.Chain, rpcURL string) (string, error) {
		if record.AdapterType != models.AdapterTypeBitcoin {
			return "", nil
		}
		return BitcoinNetworkOfRPCURL(rpcURL), nil
	}

	_, err := BuildPlan(context.Background(), models.ChainNetworkProfileMainnet, store, plainDecrypt, bitcoinOnly)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCNetworkMismatch), err)
	assert.Contains(t, err.Error(), models.NetworkBitcoinTestnet4)
}

func TestPlan_Refuses_ASolanaRecordWhoseRPCIsMainnet(t *testing.T) {
	store := vaultTestRegistry()
	sol := store.chains[models.ChainSOL]
	sol.RpcURL = solanaMainnet
	store.chains[models.ChainSOL] = sol

	_, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, nil)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCNetworkMismatch), err)
}

func TestPlan_Warns_WhenTheProbeCannotTell(t *testing.T) {
	store := vaultTestRegistry()
	btc := store.chains[models.ChainBTC]
	btc.RpcURL = "https://bitcoind.internal:8332"
	store.chains[models.ChainBTC] = btc

	plan, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, staticProbe)

	require.NoError(t, err)
	require.Len(t, plan.Warnings, 1)
	assert.Contains(t, plan.Warnings[0], models.ChainBTC)
}

func TestPlan_Warns_AboutAddedChainsMissingFromTheRegistry(t *testing.T) {
	store := vaultTestRegistry()
	delete(store.chains, models.ChainBase)
	delete(store.chains, models.ChainBSC)

	plan, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, staticProbe)

	require.NoError(t, err)
	require.Len(t, plan.Warnings, 2)
	assert.Contains(t, plan.Warnings[0], models.ChainBase)
	assert.Contains(t, plan.Warnings[1], models.ChainBSC)
	assert.Contains(t, plan.Warnings[0], "chains:add-missing")
	assert.NotContains(t, changeByChain(plan), models.ChainArbitrum)
}

func TestPlan_Rejects_UnknownProfileAndMissingDependencies(t *testing.T) {
	ctx := context.Background()
	_, err := BuildPlan(ctx, "staging", vaultTestRegistry(), plainDecrypt, nil)
	assert.Error(t, err)
	_, err = BuildPlan(ctx, models.ChainNetworkProfileTestnet, nil, plainDecrypt, nil)
	assert.Error(t, err)
	_, err = BuildPlan(ctx, models.ChainNetworkProfileTestnet, vaultTestRegistry(), nil, nil)
	assert.Error(t, err)
}

// staticProbe stands in for ProbeRPCNetwork without network calls: EVM records
// answer with their own id, the others go through the URL rules.
func staticProbe(ctx context.Context, record models.Chain, rpcURL string) (string, error) {
	if record.AdapterType != models.AdapterTypeEVM {
		return ProbeRPCNetwork(ctx, record, rpcURL)
	}
	switch rpcURL {
	case baseSepoliaRPC:
		return models.NetworkBaseSepolia, nil
	case arbSepoliaRPC:
		return models.NetworkArbitrumSepolia, nil
	case bscTestnetRPC:
		return models.NetworkBSCTestnet, nil
	}
	if strings.Contains(rpcURL, "sepolia") {
		return models.NetworkEthereumSepolia, nil
	}
	return models.NetworkPolygonAmoy, nil
}

func TestProbeNamesTronAndLitecoinNetworksFromTheirURLs(t *testing.T) {
	ctx := context.Background()
	tron := models.Chain{ID: models.ChainTron, AdapterType: models.AdapterTypeTron}
	ltc := models.Chain{ID: models.ChainLTC, AdapterType: models.AdapterTypeBitcoin}
	cases := []struct {
		record models.Chain
		rpcURL string
		want   string
	}{
		{tron, "https://nile.trongrid.io", models.NetworkTronNile},
		{tron, "https://api.nileex.io", models.NetworkTronNile},
		{tron, "https://api.shasta.trongrid.io", NetworkTronShasta},
		{tron, "https://api.trongrid.io", models.NetworkTronMainnet},
		{tron, "https://tron.internal:8090", ""},
		{tron, "not a url", ""},
		{ltc, "https://litecoinspace.org/testnet/api", models.NetworkLitecoinTestnet},
		{ltc, "https://litecoinspace.org/api", models.NetworkLitecoinMainnet},
		{ltc, "https://blockstream.info/testnet/api", ""},
		{models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin}, "https://litecoinspace.org/testnet/api", ""},
	}
	for _, tc := range cases {
		got, err := ProbeRPCNetwork(ctx, tc.record, tc.rpcURL)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "%s on %s", tc.record.ID, tc.rpcURL)
	}
}

func TestTestnetPlanRefusesATronRecordOnMainnet(t *testing.T) {
	store := vaultTestRegistry()
	tron := store.chains[models.ChainTron]
	tron.RpcURL = "https://api.trongrid.io"
	store.chains[models.ChainTron] = tron

	_, err := BuildPlan(context.Background(), models.ChainNetworkProfileTestnet, store, plainDecrypt, staticProbe)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRPCNetworkMismatch), err)
	assert.Contains(t, err.Error(), models.NetworkTronMainnet)
}

func TestAccount_Moves_ToTheProfileEnvironment(t *testing.T) {
	store := vaultTestRegistry()
	accountID := uuid.New()
	store.accounts[accountID] = models.Account{ID: accountID, Environment: models.EnvironmentProd}

	change, err := PlanAccountEnvironment(context.Background(), store, accountID, models.ChainNetworkProfileTestnet)
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Equal(t, models.EnvironmentTest, change.To)

	require.NoError(t, ApplyAccountChange(context.Background(), store, change))
	again, err := PlanAccountEnvironment(context.Background(), store, accountID, models.ChainNetworkProfileTestnet)
	require.NoError(t, err)
	assert.Nil(t, again)
}

func TestAccount_Paired_WithATestAccountIsNotMovedToTest(t *testing.T) {
	store := vaultTestRegistry()
	prodID, testID := uuid.New(), uuid.New()
	store.accounts[prodID] = models.Account{ID: prodID, Environment: models.EnvironmentProd, LinkedAccountID: &testID}
	store.accounts[testID] = models.Account{ID: testID, Environment: models.EnvironmentTest, LinkedAccountID: &prodID}

	_, err := PlanAccountEnvironment(context.Background(), store, prodID, models.ChainNetworkProfileTestnet)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "paired")
}

func TestAccount_Plan_RejectsMissingAccounts(t *testing.T) {
	store := vaultTestRegistry()
	_, err := PlanAccountEnvironment(context.Background(), store, uuid.Nil, models.ChainNetworkProfileTestnet)
	assert.Error(t, err)
	_, err = PlanAccountEnvironment(context.Background(), store, uuid.New(), models.ChainNetworkProfileTestnet)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, ApplyAccountChange(context.Background(), store, nil))
}

func btcWalletWithMainnetAddresses(store *fakeStore) (models.Wallet, models.Address, models.Address) {
	wallet := models.Wallet{ID: uuid.New(), Chain: models.ChainBTC, Label: "btc_deposit", MPCPublicKey: compressedPub}
	genesis := models.Address{ID: uuid.New(), WalletID: wallet.ID, Chain: models.ChainBTC, Address: mainnetGenesis,
		DerivationType: GenesisDerivationType, ExternalUserID: GenesisExternalUserID, IsActive: true}
	user := models.Address{ID: uuid.New(), WalletID: wallet.ID, Chain: models.ChainBTC, Address: "bc1quser",
		DerivationIndex: 3, DerivationType: "bip32", ExternalUserID: "3119", IsActive: true}
	store.wallets[models.ChainBTC] = []models.Wallet{wallet}
	store.addresses[wallet.ID] = []models.Address{genesis, user}
	return wallet, genesis, user
}

func TestBitcoin_Moved_ToTestnetRetiresBc1AndReissuesTheGenesisAsTb1(t *testing.T) {
	store := vaultTestRegistry()
	wallet, _, _ := btcWalletWithMainnetAddresses(store)
	ctx := context.Background()

	alignment, err := PlanAlignment(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil, uuid.Nil)
	require.NoError(t, err)

	reissues := alignment.Reissues[models.ChainBTC]
	require.Len(t, reissues, 1)
	assert.Len(t, reissues[0].Retired, 2)
	pub, _ := hex.DecodeString(compressedPub)
	wantGenesis, _ := addressing.DeriveBtcAddress(addressing.BtcHRPTestnet, pub)
	assert.Equal(t, wantGenesis, reissues[0].Genesis.Address)
	assert.True(t, strings.HasPrefix(wantGenesis, "tb1q"))
	assert.Equal(t, GenesisDerivationIndex, reissues[0].Genesis.DerivationIndex)
	assert.Equal(t, wallet.ID, reissues[0].Genesis.WalletID)

	require.NoError(t, ApplyAlignment(ctx, store, alignment))
	active, _ := store.ActiveAddressesOfWallet(ctx, wallet.ID)
	require.Len(t, active, 1)
	assert.Equal(t, wantGenesis, active[0].Address)

	again, err := PlanAlignment(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil, uuid.Nil)
	require.NoError(t, err)
	assert.True(t, again.IsEmpty(), "a second run changes nothing")
}

func TestRepeated_Run_ReissuesAddressesLeftBehindByAStoppedRun(t *testing.T) {
	store := vaultTestRegistry()
	wallet, _, _ := btcWalletWithMainnetAddresses(store)
	ctx := context.Background()
	plan, err := BuildPlan(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil)
	require.NoError(t, err)
	require.NoError(t, ApplyPlan(ctx, store, plan))

	alignment, err := PlanAlignment(ctx, models.ChainNetworkProfileTestnet, store, plainDecrypt, nil, uuid.Nil)
	require.NoError(t, err)

	assert.Empty(t, alignment.Plan.Changes)
	require.Len(t, alignment.Reissues[models.ChainBTC], 1)
	assert.Equal(t, wallet.ID, alignment.Reissues[models.ChainBTC][0].WalletID)
}

func TestReissue_Leaves_EVMAndSolanaAddressesAlone(t *testing.T) {
	store := vaultTestRegistry()
	for _, target := range []ReissueTarget{
		{ChainID: models.ChainETH, AdapterType: models.AdapterTypeEVM, Testnet: true},
		{ChainID: models.ChainSOL, AdapterType: models.AdapterTypeSolana, Testnet: true},
	} {
		reissues, err := PlanAddressReissue(context.Background(), store, target)
		require.NoError(t, err)
		assert.Empty(t, reissues, target.ChainID)
	}
	_, err := PlanAddressReissue(context.Background(), nil, ReissueTarget{})
	assert.Error(t, err)
}

func TestReissue_Rejects_AWalletWithAnUnreadablePublicKey(t *testing.T) {
	store := vaultTestRegistry()
	wallet, _, _ := btcWalletWithMainnetAddresses(store)
	wallet.MPCPublicKey = "not-hex"
	store.wallets[models.ChainBTC] = []models.Wallet{wallet}

	_, err := PlanAddressReissue(context.Background(), store, ReissueTarget{ChainID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, Testnet: true})

	assert.Error(t, err)
}
