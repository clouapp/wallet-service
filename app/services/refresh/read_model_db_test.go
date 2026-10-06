package refresh

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// chainBalances answers GetBalance per address, like an RPC would for base addresses.
func nativeBalanceChain(chainID string, decimals uint8, human string, byAddress map[string]*big.Int) *mocks.MockChain {
	adapter := mocks.NewMockChain(chainID)
	adapter.GetBalanceFn = func(_ context.Context, address string) (*types.Balance, error) {
		value, ok := byAddress[address]
		if !ok {
			value = big.NewInt(0)
		}
		return &types.Balance{Address: address, Asset: chainID, Amount: value, Decimals: decimals, Human: human}, nil
	}
	return adapter
}

// memReadModel is the balance-refresh ports: wallet rows, asset balances, snapshots and sync state.
type memReadModel struct {
	mu      sync.Mutex
	wallets map[uuid.UUID]*models.Wallet
	order   []uuid.UUID
	assets  map[uuid.UUID][]models.WalletAssetBalance
	sync    map[uuid.UUID]models.WalletSyncState
}

func newMemReadModel() *memReadModel {
	return &memReadModel{
		wallets: map[uuid.UUID]*models.Wallet{},
		assets:  map[uuid.UUID][]models.WalletAssetBalance{},
		sync:    map[uuid.UUID]models.WalletSyncState{},
	}
}

func (m *memReadModel) add(wallet *models.Wallet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wallets[wallet.ID] = wallet
	m.order = append(m.order, wallet.ID)
}

func (m *memReadModel) FindByID(_ context.Context, id uuid.UUID) (*models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.wallets[id]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return wallet, nil
}

func (m *memReadModel) FindAll(context.Context) ([]models.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.Wallet, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, *m.wallets[id])
	}
	return out, nil
}

func (m *memReadModel) SetBalanceSummary(_ context.Context, id uuid.UUID, asset, raw, display string, syncedAt time.Time, readModelStatus string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wallet, ok := m.wallets[id]
	if !ok {
		return models.ErrRepositoryNotFound
	}
	wallet.BalanceAsset = &asset
	wallet.BalanceRaw = &raw
	wallet.BalanceDisplay = &display
	wallet.BalanceLastSyncedAt = &syncedAt
	wallet.ReadModelStatus = readModelStatus
	return nil
}

func (m *memReadModel) ReplaceForWallet(_ context.Context, walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error {
	for i := range rows {
		if err := rows[i].ValidateAmounts(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]models.WalletAssetBalance, 0, len(m.assets[walletID]))
	for _, row := range m.assets[walletID] {
		if row.ChainID != chainID {
			kept = append(kept, row)
		}
	}
	m.assets[walletID] = append(kept, rows...)
	return nil
}

func (m *memReadModel) assetsFor(walletID uuid.UUID) []models.WalletAssetBalance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]models.WalletAssetBalance(nil), m.assets[walletID]...)
}

func (m *memReadModel) Create(_ context.Context, snapshot *models.WalletBalanceSnapshot) error {
	if snapshot == nil {
		return fmtErr("snapshot is nil")
	}
	if err := snapshot.ValidateAmounts(); err != nil {
		return err
	}
	return nil
}

func (m *memReadModel) TrimToLatest(context.Context, uuid.UUID, string, int) error { return nil }

func (m *memReadModel) Upsert(_ context.Context, state *models.WalletSyncState) error {
	if state == nil {
		return fmtErr("sync state is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sync[state.WalletID] = *state
	return nil
}

func (m *memReadModel) UpdateFailure(_ context.Context, walletID uuid.UUID, chainID, scope, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sync[walletID] = models.WalletSyncState{
		WalletID:  walletID,
		ChainID:   chainID,
		SyncScope: scope,
		Status:    string(types.SyncStatusFailed),
		LastError: &errMsg,
	}
	return nil
}

func (m *memReadModel) syncState(walletID uuid.UUID) (models.WalletSyncState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sync[walletID]
	return state, ok
}

func fmtErr(text string) error { return errors.New(text) }

func newDBRefresher(t *testing.T, registry *chain.Registry, store *memReadModel) *WalletRefresher {
	t.Helper()
	balances := NewBalanceService(Deps{
		Registry:      registry,
		Wallets:       store,
		AssetBalances: store,
		Snapshots:     store,
		SyncStates:    store,
	})
	refresher, err := NewWalletRefresher(WalletRefresherDeps{
		Balances: balances,
		Wallets:  store,
		Chains:   registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	return refresher
}

func watchedWallet(chainID, address string) *models.Wallet {
	return &models.Wallet{
		ID:             uuid.New(),
		Chain:          chainID,
		Status:         "active",
		DepositAddress: &models.Address{ID: uuid.New(), Chain: chainID, Address: address},
	}
}

func TestRefresh_All_FillsTheReadModelForEthBtcAndSolFromTheBaseAddress(t *testing.T) {
	store := newMemReadModel()
	solWallet := watchedWallet("sol", "SolBase")
	btcWallet := watchedWallet("btc", "BtcBase")
	ethWallet := watchedWallet("eth", "EthBase")
	store.add(solWallet)
	store.add(btcWallet)
	store.add(ethWallet)

	registry := chain.NewRegistry()
	registry.RegisterChain(nativeBalanceChain("sol", 9, "0.02", map[string]*big.Int{"SolBase": big.NewInt(20_000_000), "SolChild": big.NewInt(5_000_000_000)}))
	registry.RegisterChain(nativeBalanceChain("btc", 8, "0.00337841", map[string]*big.Int{"BtcBase": big.NewInt(337_841)}))
	registry.RegisterChain(nativeBalanceChain("eth", 18, "0.001958106721664", map[string]*big.Int{"EthBase": big.NewInt(1_958_106_721_664_000)}))

	summary, err := newDBRefresher(t, registry, store).RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Refreshed != 3 || summary.Failed != 0 {
		t.Fatalf("expected the three wallets refreshed, got %+v", summary)
	}
	for wallet, want := range map[*models.Wallet]string{solWallet: "20000000", btcWallet: "337841", ethWallet: "1958106721664000"} {
		if wallet.ReadModelStatus != string(types.ReadModelSynced) || wallet.BalanceRaw == nil || *wallet.BalanceRaw != want {
			t.Fatalf("%s: expected a synced base-address balance %s, got status %s raw %v", wallet.Chain, want, wallet.ReadModelStatus, wallet.BalanceRaw)
		}
		rows := store.assetsFor(wallet.ID)
		if len(rows) != 1 || rows[0].AmountRaw != want || rows[0].AssetType != string(types.AssetTypeNative) {
			t.Fatalf("%s: expected one native asset row of %s, got %+v", wallet.Chain, want, rows)
		}
	}
}

func TestRefresh_Wallet_NegativeChainAmountIsRejectedAndRecordedAsAFailure(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	registry := chain.NewRegistry()
	adapter := mocks.NewMockChain("sol")
	adapter.GetBalanceFn = func(_ context.Context, address string) (*types.Balance, error) {
		return &types.Balance{Address: address, Asset: "sol", Amount: big.NewInt(-5), Decimals: 9, Human: "-0.000000005"}, nil
	}
	registry.RegisterChain(adapter)

	err := newDBRefresher(t, registry, store).RefreshWalletByID(context.Background(), wallet.ID)
	if !errors.Is(err, amount.ErrNegativeAmount) {
		t.Fatalf("expected the amount guard to reject a negative balance, got %v", err)
	}
	if rows := store.assetsFor(wallet.ID); len(rows) != 0 {
		t.Fatalf("no negative row may be stored, got %+v", rows)
	}
	if wallet.BalanceRaw != nil && *wallet.BalanceRaw != "" {
		t.Fatalf("the wallet summary must stay untouched, got %v", *wallet.BalanceRaw)
	}
	state, ok := store.syncState(wallet.ID)
	if !ok {
		t.Fatal("expected the failure recorded in the sync state")
	}
	if state.Status != string(types.SyncStatusFailed) || state.SyncScope != string(RefreshScopeBalances) {
		t.Fatalf("expected the failure recorded in the sync state, got %+v", state)
	}
}
