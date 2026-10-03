package refresh

import (
	"context"
	"errors"
	"math/big"
	"os"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}

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

func newDBRefresher(t *testing.T, registry *chain.Registry) *WalletRefresher {
	t.Helper()
	balances := NewBalanceService(registry, repositories.NewWalletRepository(nil), repositories.NewWalletAssetBalanceRepository(nil),
		repositories.NewWalletBalanceSnapshotRepository(nil), repositories.NewWalletSyncStateRepository(nil))
	refresher, err := NewWalletRefresher(balances, repositories.NewWalletRepository(nil), registry, 0)
	if err != nil {
		t.Fatal(err)
	}
	return refresher
}

// insertChain adds the chains row the balance tables reference.
func insertChain(t *testing.T, id, adapterType, nativeSymbol string, nativeDecimals int) {
	t.Helper()
	row := models.Chain{
		ID:                    id,
		Name:                  id,
		AdapterType:           adapterType,
		NativeSymbol:          nativeSymbol,
		NativeDecimals:        nativeDecimals,
		IsTestnet:             true,
		RequiredConfirmations: 1,
		Status:                "active",
	}
	if err := facades.Orm().Query().Create(&row); err != nil {
		t.Fatalf("insert chain %s: %v", id, err)
	}
}

func reloadWallet(t *testing.T, wallet models.Wallet) models.Wallet {
	t.Helper()
	var stored models.Wallet
	if err := facades.Orm().Query().Where("id = ?", wallet.ID).First(&stored); err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestRefreshAll_FillsTheReadModelForEthBtcAndSolFromTheBaseAddress(t *testing.T) {
	mocks.TestDB(t)
	insertChain(t, "sol", models.AdapterTypeSolana, "sol", 9)
	insertChain(t, "btc", models.AdapterTypeBitcoin, "btc", 8)
	insertChain(t, "eth", models.AdapterTypeEVM, "eth", 18)
	solWallet := mocks.InsertWallet(t, "sol")
	btcWallet := mocks.InsertWallet(t, "btc")
	ethWallet := mocks.InsertWallet(t, "eth")
	solChild := mocks.InsertAddress(t, solWallet.ID, "sol", "SolChildAddress", "user", 1)

	solBase := reloadWithAddress(t, solWallet).DepositAddress.Address
	btcBase := reloadWithAddress(t, btcWallet).DepositAddress.Address
	ethBase := reloadWithAddress(t, ethWallet).DepositAddress.Address
	registry := chain.NewRegistry()
	registry.RegisterChain(nativeBalanceChain("sol", 9, "0.02", map[string]*big.Int{solBase: big.NewInt(20_000_000), solChild.Address: big.NewInt(5_000_000_000)}))
	registry.RegisterChain(nativeBalanceChain("btc", 8, "0.00337841", map[string]*big.Int{btcBase: big.NewInt(337_841)}))
	registry.RegisterChain(nativeBalanceChain("eth", 18, "0.001958106721664", map[string]*big.Int{ethBase: big.NewInt(1_958_106_721_664_000)}))

	summary, err := newDBRefresher(t, registry).RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Refreshed != 3 || summary.Failed != 0 {
		t.Fatalf("expected the three wallets refreshed, got %+v", summary)
	}
	for wallet, want := range map[models.Wallet]string{solWallet: "20000000", btcWallet: "337841", ethWallet: "1958106721664000"} {
		stored := reloadWallet(t, wallet)
		if stored.ReadModelStatus != string(types.ReadModelSynced) || stored.BalanceRaw == nil || *stored.BalanceRaw != want {
			t.Fatalf("%s: expected a synced base-address balance %s, got status %s raw %v", wallet.Chain, want, stored.ReadModelStatus, stored.BalanceRaw)
		}
		rows, err := repositories.NewWalletAssetBalanceRepository(nil).ListByWallet(context.Background(), wallet.ID)
		if err != nil || len(rows) != 1 || rows[0].AmountRaw != want || rows[0].AssetType != string(types.AssetTypeNative) {
			t.Fatalf("%s: expected one native asset row of %s, got %+v, %v", wallet.Chain, want, rows, err)
		}
	}
}

func TestRefreshWallet_NegativeChainAmountIsRejectedAndRecordedAsAFailure(t *testing.T) {
	mocks.TestDB(t)
	insertChain(t, "sol", models.AdapterTypeSolana, "sol", 9)
	wallet := mocks.InsertWallet(t, "sol")
	registry := chain.NewRegistry()
	adapter := mocks.NewMockChain("sol")
	adapter.GetBalanceFn = func(_ context.Context, address string) (*types.Balance, error) {
		return &types.Balance{Address: address, Asset: "sol", Amount: big.NewInt(-5), Decimals: 9, Human: "-0.000000005"}, nil
	}
	registry.RegisterChain(adapter)

	err := newDBRefresher(t, registry).RefreshWalletByID(context.Background(), wallet.ID)
	if !errors.Is(err, amount.ErrNegativeAmount) {
		t.Fatalf("expected the amount guard to reject a negative balance, got %v", err)
	}
	if rows, _ := repositories.NewWalletAssetBalanceRepository(nil).ListByWallet(context.Background(), wallet.ID); len(rows) != 0 {
		t.Fatalf("no negative row may be stored, got %+v", rows)
	}
	if stored := reloadWallet(t, wallet); stored.BalanceRaw != nil && *stored.BalanceRaw != "" {
		t.Fatalf("the wallet summary must stay untouched, got %v", *stored.BalanceRaw)
	}
	var state models.WalletSyncState
	if err := facades.Orm().Query().Where("wallet_id = ? AND sync_scope = ?", wallet.ID, string(RefreshScopeBalances)).First(&state); err != nil {
		t.Fatal(err)
	}
	if state.Status != string(types.SyncStatusFailed) {
		t.Fatalf("expected the failure recorded in the sync state, got %+v", state)
	}
}

func reloadWithAddress(t *testing.T, wallet models.Wallet) *models.Wallet {
	t.Helper()
	stored, err := repositories.NewWalletRepository(nil).FindByID(context.Background(), wallet.ID)
	if err != nil || stored == nil || stored.DepositAddress == nil {
		t.Fatalf("load wallet with its base address: %v", err)
	}
	return stored
}
