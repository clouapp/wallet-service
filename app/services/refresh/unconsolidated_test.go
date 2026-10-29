package refresh

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const testUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// recordedLedger answers the ledger of one wallet as the transactions table would.
type recordedLedger struct {
	entries []models.AddressLedgerEntry
	fees    []models.AddressFeeEntry
	err     error
}

func (l *recordedLedger) WalletAddressLedger(context.Context, uuid.UUID, string) ([]models.AddressLedgerEntry, error) {
	return l.entries, l.err
}

func (l *recordedLedger) WalletAddressFees(context.Context, uuid.UUID, string, bool) ([]models.AddressFeeEntry, error) {
	return l.fees, l.err
}

// heldChain answers what each address holds, native and USDC, and records every read.
type heldChain struct {
	*mocks.MockChain
	mu     sync.Mutex
	native map[string]*big.Int
	usdc   map[string]*big.Int
	reads  []string
	fail   map[string]error
}

func newHeldChain() *heldChain {
	held := &heldChain{MockChain: mocks.NewMockChain("sol"), native: map[string]*big.Int{}, usdc: map[string]*big.Int{}, fail: map[string]error{}}
	held.GetBalanceFn = func(_ context.Context, address string) (*types.Balance, error) {
		return held.read(address, "native", held.native, 9)
	}
	held.GetTokenBalanceFn = func(_ context.Context, address string, token types.Token) (*types.Balance, error) {
		return held.read(address, token.Symbol, held.usdc, token.Decimals)
	}
	return held
}

func (h *heldChain) read(address, asset string, amounts map[string]*big.Int, decimals uint8) (*types.Balance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads = append(h.reads, address+":"+asset)
	if err := h.fail[address]; err != nil {
		return nil, err
	}
	value := amounts[address]
	if value == nil {
		value = big.NewInt(0)
	}
	return &types.Balance{Address: address, Asset: asset, Amount: new(big.Int).Set(value), Decimals: decimals, Human: value.String()}, nil
}

func (h *heldChain) readsOf(address string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, read := range h.reads {
		if len(read) > len(address) && read[:len(address)+1] == address+":" {
			out = append(out, read)
		}
	}
	return out
}

func newLedgerRefresher(t *testing.T, adapter types.Chain, store *memReadModel, ledger AddressLedgerStore) *WalletRefresher {
	t.Helper()
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	registry.RegisterToken(types.Token{ChainID: "sol", Symbol: "USDC", Name: "USD Coin", Contract: testUSDCMint, Decimals: 6})
	balances := NewBalanceService(Deps{
		Registry:      registry,
		Wallets:       store,
		AssetBalances: store,
		Snapshots:     store,
		SyncStates:    store,
		Ledger:        ledger,
	})
	refresher, err := NewWalletRefresher(WalletRefresherDeps{Balances: balances, Wallets: store, Chains: registry})
	if err != nil {
		t.Fatal(err)
	}
	return refresher
}

func rowOf(t *testing.T, rows []models.WalletAssetBalance, assetType string) models.WalletAssetBalance {
	t.Helper()
	for _, row := range rows {
		if row.AssetType == assetType {
			return row
		}
	}
	t.Fatalf("no %s row in %+v", assetType, rows)
	return models.WalletAssetBalance{}
}

// The production case of 2026-10-29: a Markets user's deposit address received
// 3 × 0.01 SOL and 0.0022 USDC, the base holds 0.05 SOL, and the wallet showed
// 0.05 SOL and 0 USDC. Another child was already consolidated.
func TestRefresh_Wallet_AddsWhatTheOtherDepositAddressesHoldBesideTheBase(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	adapter := newHeldChain()
	adapter.native["SolBase"] = big.NewInt(50_000_000)
	adapter.native["SolChild"] = big.NewInt(29_995_000)
	adapter.usdc["SolChild"] = big.NewInt(2_200)
	ledger := &recordedLedger{
		entries: []models.AddressLedgerEntry{
			{Address: "SolBase", Asset: "sol", Credited: "50000000", Debited: "0"},
			{Address: "SolChild", Asset: "sol", Credited: "30000000", Debited: "0"},
			{Address: "SolChild", Asset: "USDC", TokenContract: testUSDCMint, Credited: "2200", Debited: "0"},
			{Address: "SolSwept", Asset: "sol", Credited: "10000000", Debited: "9995000"},
			{Address: "SolChild", Asset: "BONK", TokenContract: "UnlistedMint", Credited: "7", Debited: "0"},
		},
		fees: []models.AddressFeeEntry{{Address: "SolSwept", Fees: "5000"}},
	}

	if err := newLedgerRefresher(t, adapter, store, ledger).RefreshWalletByID(context.Background(), wallet.ID); err != nil {
		t.Fatal(err)
	}
	rows := store.assetsFor(wallet.ID)
	native := rowOf(t, rows, string(types.AssetTypeNative))
	if native.AmountRaw != "50000000" {
		t.Fatalf("amount_raw must stay the base address's balance, got %s", native.AmountRaw)
	}
	if native.UnconsolidatedRaw != "29995000" || native.UnconsolidatedDisplay != "0.029995" || native.UnconsolidatedAddresses != 1 {
		t.Fatalf("expected the child's SOL read on chain, got %s / %s in %d", native.UnconsolidatedRaw, native.UnconsolidatedDisplay, native.UnconsolidatedAddresses)
	}
	usdc := rowOf(t, rows, string(types.AssetTypeToken))
	if usdc.UnconsolidatedRaw != "2200" || usdc.UnconsolidatedDisplay != "0.0022" || usdc.UnconsolidatedAddresses != 1 {
		t.Fatalf("expected the child's USDC, got %s / %s in %d", usdc.UnconsolidatedRaw, usdc.UnconsolidatedDisplay, usdc.UnconsolidatedAddresses)
	}
	if reads := adapter.readsOf("SolSwept"); len(reads) != 0 {
		t.Fatalf("a consolidated address must cost no read, got %v", reads)
	}
	if reads := adapter.readsOf("SolChild"); len(reads) != 2 {
		t.Fatalf("the child is read once per asset it holds, got %v", reads)
	}
}

func TestRefresh_Wallet_TheLedgerStandsInForAReadThatFails(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	adapter := newHeldChain()
	adapter.fail["ChildA"] = errors.New("connection reset")
	adapter.usdc["ChildB"] = big.NewInt(1_000)
	ledger := &recordedLedger{entries: []models.AddressLedgerEntry{
		{Address: "ChildA", Asset: "USDC", TokenContract: testUSDCMint, Credited: "2200", Debited: "0"},
		{Address: "ChildB", Asset: "USDC", TokenContract: testUSDCMint, Credited: "1000", Debited: "0"},
	}}

	if err := newLedgerRefresher(t, adapter, store, ledger).RefreshWalletByID(context.Background(), wallet.ID); err != nil {
		t.Fatal(err)
	}
	usdc := rowOf(t, store.assetsFor(wallet.ID), string(types.AssetTypeToken))
	if usdc.UnconsolidatedRaw != "3200" || usdc.UnconsolidatedAddresses != 2 {
		t.Fatalf("expected the failed read counted at its ledger amount, got %s in %d", usdc.UnconsolidatedRaw, usdc.UnconsolidatedAddresses)
	}
}

func TestRefresh_Wallet_ARateLimitLeavesTheRestOfThePassToTheLedger(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	adapter := newHeldChain()
	adapter.fail["Child0"] = fmt.Errorf("getTokenAccountsByOwner: %w", chain.ErrRateLimited)
	var entries []models.AddressLedgerEntry
	for i := range 5 {
		entries = append(entries, models.AddressLedgerEntry{Address: fmt.Sprintf("Child%d", i), Asset: "USDC", TokenContract: testUSDCMint, Credited: "100", Debited: "0"})
	}

	if err := newLedgerRefresher(t, adapter, store, &recordedLedger{entries: entries}).RefreshWalletByID(context.Background(), wallet.ID); err != nil {
		t.Fatal(err)
	}
	usdc := rowOf(t, store.assetsFor(wallet.ID), string(types.AssetTypeToken))
	if usdc.UnconsolidatedRaw != "500" || usdc.UnconsolidatedAddresses != 5 {
		t.Fatalf("expected every child at its ledger amount, got %s in %d", usdc.UnconsolidatedRaw, usdc.UnconsolidatedAddresses)
	}
	for i := 1; i < 5; i++ {
		if reads := adapter.readsOf(fmt.Sprintf("Child%d", i)); len(reads) != 0 {
			t.Fatalf("no read may follow a rate limit, got %v", reads)
		}
	}
}

func TestRefresh_Wallet_AnEmptiedChildIsNotCounted(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	adapter := newHeldChain()
	// The ledger has not seen the sweep confirm yet; the chain says the child is empty.
	ledger := &recordedLedger{entries: []models.AddressLedgerEntry{
		{Address: "SolChild", Asset: "USDC", TokenContract: testUSDCMint, Credited: "2200", Debited: "0", InFlight: 1},
	}}

	if err := newLedgerRefresher(t, adapter, store, ledger).RefreshWalletByID(context.Background(), wallet.ID); err != nil {
		t.Fatal(err)
	}
	usdc := rowOf(t, store.assetsFor(wallet.ID), string(types.AssetTypeToken))
	if usdc.UnconsolidatedRaw != "0" || usdc.UnconsolidatedDisplay != "0" || usdc.UnconsolidatedAddresses != 0 {
		t.Fatalf("expected nothing unconsolidated, got %s / %s in %d", usdc.UnconsolidatedRaw, usdc.UnconsolidatedDisplay, usdc.UnconsolidatedAddresses)
	}
}

func TestRefresh_Wallet_ALedgerThatCannotBeReadFailsTheRefresh(t *testing.T) {
	store := newMemReadModel()
	wallet := watchedWallet("sol", "SolBase")
	store.add(wallet)
	ledger := &recordedLedger{err: errors.New("database is down")}

	err := newLedgerRefresher(t, newHeldChain(), store, ledger).RefreshWalletByID(context.Background(), wallet.ID)
	if err == nil {
		t.Fatal("expected the refresh to fail rather than store a total without the deposit addresses")
	}
	if rows := store.assetsFor(wallet.ID); len(rows) != 0 {
		t.Fatalf("no row may be stored, got %+v", rows)
	}
	if state, ok := store.syncState(wallet.ID); !ok || state.Status != string(types.SyncStatusFailed) {
		t.Fatalf("expected the failure recorded, got %+v", state)
	}
}
