package chain

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	tronBlockWithUSDT   = uint64(71532743)
	tronBlockWithRevert = uint64(71532514)
	tronUSDTDepositTxID = "c4c6e63a0b62738d94460c5b8066942c3445b91d4cd22e10d97ba5a06603cd6e"
)

func tronBase58(t *testing.T, hexAddress string) string {
	t.Helper()
	address, err := addressing.TronAddressFromHex(hexAddress)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func loadTronBlockFixtures(node *fakeTronNode, t *testing.T) {
	t.Helper()
	for _, number := range []uint64{tronBlockWithUSDT, tronBlockWithRevert} {
		node.blocks[number] = readTronFixture(t, "tron_getblockbynum_"+itoa(number)+".json")
		node.infos[number] = readTronFixture(t, "tron_gettransactioninfobyblocknum_"+itoa(number)+".json")
	}
}

func itoa(n uint64) string { return new(big.Int).SetUint64(n).String() }

// mutateTronFixture rewrites a fixture through edit, for synthetic variants of
// real blocks.
func mutateTronFixture(t *testing.T, fixture json.RawMessage, edit func(any)) json.RawMessage {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(fixture, &decoded); err != nil {
		t.Fatal(err)
	}
	edit(decoded)
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestTronScanBlockTRXAndTRC20Deposits(t *testing.T) {
	node := newFakeTronNode(t)
	loadTronBlockFixtures(node, t)
	adapter := newTronTestAdapter(t, node)

	transfers, err := adapter.ScanBlock(context.Background(), tronBlockWithUSDT)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 8 {
		t.Fatalf("got %d transfers, want 7 TRX + 1 USDT", len(transfers))
	}
	wantTRX := []int64{74_000, 728_000, 772_000, 230_000, 294_000, 488_000, 604_000}
	blockTime := time.UnixMilli(1791126744000)
	sender := tronBase58(t, "41f7c3feccb6461aab0fd25f61d9560645b08228cb")
	recipient := tronBase58(t, "41b06b4139895c9f51c967c9f3d9089ca721e8e34c")
	for i, amount := range wantTRX {
		got := transfers[i]
		if got.Asset != models.NativeTRX || got.Token != nil || got.Amount.Int64() != amount ||
			got.From != sender || got.To != recipient ||
			got.BlockNumber != tronBlockWithUSDT || !got.Timestamp.Equal(blockTime) ||
			got.BlockHash != "00000000044380c7b08869904009d6ab7d940984fcf998e8bce36814d865db23" {
			t.Fatalf("TRX transfer %d = %+v", i, got)
		}
	}
	usdt := transfers[7]
	if usdt.TxHash != tronUSDTDepositTxID || usdt.Token == nil || usdt.Token.Contract != models.USDTContractTronNile ||
		usdt.Asset != models.SymbolUSDT || usdt.Amount.Int64() != 5_000_000 || usdt.LogIndex != 0 ||
		usdt.From != "THwaqc496gjyFLqrvVeUNTCWY7Vn9RH995" || usdt.To != "TL1eeYCiqPwERsRVdscUBEWi5aHvnwtvfh" {
		t.Fatalf("USDT transfer = %+v", usdt)
	}
}

func TestTronScanBlockIgnoresFailedTransactions(t *testing.T) {
	node := newFakeTronNode(t)
	loadTronBlockFixtures(node, t)
	adapter := newTronTestAdapter(t, node)

	// Real block: a reverted contract call (9e542921…) next to seven TRX transfers.
	transfers, err := adapter.ScanBlock(context.Background(), tronBlockWithRevert)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 7 {
		t.Fatalf("got %d transfers, want the 7 TRX transfers", len(transfers))
	}

	// Synthetic: the real USDT deposit's receipt marked REVERT (logs kept) and the
	// first TRX transfer's ret marked failed.
	node.infos[tronBlockWithUSDT] = mutateTronFixture(t, node.infos[tronBlockWithUSDT], func(v any) {
		info := v.([]any)[5].(map[string]any)
		info["result"] = "FAILED"
		info["receipt"].(map[string]any)["result"] = "REVERT"
	})
	node.blocks[tronBlockWithUSDT] = mutateTronFixture(t, node.blocks[tronBlockWithUSDT], func(v any) {
		tx := v.(map[string]any)["transactions"].([]any)[0].(map[string]any)
		tx["ret"].([]any)[0].(map[string]any)["contractRet"] = "REVERT"
	})
	transfers, err = adapter.ScanBlock(context.Background(), tronBlockWithUSDT)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 6 {
		t.Fatalf("got %d transfers, want 6 TRX and no USDT", len(transfers))
	}
	for _, transfer := range transfers {
		if transfer.Token != nil || transfer.Amount.Int64() == 74_000 {
			t.Fatalf("failed transfer reported: %+v", transfer)
		}
	}
}

func TestTronScanBlockUnregisteredTokenAndNoCalls(t *testing.T) {
	node := newFakeTronNode(t)
	loadTronBlockFixtures(node, t)
	server := newTronTestAdapter(t, node)
	adapter := NewTronLive(TronConfig{ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, RPCURL: server.cfg.RPCURL})

	transfers, err := adapter.ScanBlock(context.Background(), tronBlockWithUSDT)
	if err != nil || len(transfers) != 7 {
		t.Fatalf("no registered tokens: %d transfers, %v", len(transfers), err)
	}
	if node.callCount("/wallet/gettransactioninfobyblocknum") != 0 {
		t.Fatal("read transaction infos with no token registered")
	}

	mainnetOnly := NewTronLive(TronConfig{ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, RPCURL: server.cfg.RPCURL,
		Tokens: []types.Token{{Symbol: models.SymbolUSDT, Contract: models.USDTContractTron, Decimals: 6, ChainID: models.ChainTron}}})
	transfers, err = mainnetOnly.ScanBlock(context.Background(), tronBlockWithUSDT)
	if err != nil || len(transfers) != 7 {
		t.Fatalf("other contract registered: %d transfers, %v", len(transfers), err)
	}
}

func TestTronScanBlockFailsOnIncompleteReads(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, node *fakeTronNode)
	}{
		{"transaction infos unavailable", func(t *testing.T, node *fakeTronNode) {
			node.failPaths["/wallet/gettransactioninfobyblocknum"] = http.StatusInternalServerError
		}},
		{"transaction infos missing", func(t *testing.T, node *fakeTronNode) {
			node.infos[tronBlockWithUSDT] = json.RawMessage("{}")
		}},
		{"one transaction info short", func(t *testing.T, node *fakeTronNode) {
			node.infos[tronBlockWithUSDT] = mutateTronFixture(t, node.infos[tronBlockWithUSDT], func(v any) {})
			var infos []any
			_ = json.Unmarshal(node.infos[tronBlockWithUSDT], &infos)
			node.infos[tronBlockWithUSDT], _ = json.Marshal(infos[1:])
		}},
		{"block unknown", func(t *testing.T, node *fakeTronNode) {
			delete(node.blocks, tronBlockWithUSDT)
		}},
		{"node error member", func(t *testing.T, node *fakeTronNode) {
			node.blocks[tronBlockWithUSDT] = json.RawMessage(`{"Error":"class org.tron.core.exception.StoreException : block not found"}`)
		}},
		{"block id does not encode the number", func(t *testing.T, node *fakeTronNode) {
			node.blocks[tronBlockWithUSDT] = mutateTronFixture(t, node.blocks[tronBlockWithUSDT], func(v any) {
				v.(map[string]any)["blockID"] = tronTestHeadBlockID
			})
		}},
		{"malformed registered token log", func(t *testing.T, node *fakeTronNode) {
			node.infos[tronBlockWithUSDT] = mutateTronFixture(t, node.infos[tronBlockWithUSDT], func(v any) {
				log := v.([]any)[5].(map[string]any)["log"].([]any)[0].(map[string]any)
				log["data"] = "4c4b40"
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := newFakeTronNode(t)
			loadTronBlockFixtures(node, t)
			tc.setup(t, node)
			adapter := newTronTestAdapter(t, node)
			if transfers, err := adapter.ScanBlock(context.Background(), tronBlockWithUSDT); err == nil {
				t.Fatalf("scan succeeded with %d transfers", len(transfers))
			}
		})
	}
}

func TestTronGetTransactionBlock(t *testing.T) {
	node := newFakeTronNode(t)
	adapter := newTronTestAdapter(t, node)
	var infos []json.RawMessage
	_ = json.Unmarshal(readTronFixture(t, "tron_gettransactioninfobyblocknum_71532743.json"), &infos)
	node.txInfos[tronUSDTDepositTxID] = string(infos[5])
	var reverted []json.RawMessage
	_ = json.Unmarshal(readTronFixture(t, "tron_gettransactioninfobyblocknum_71532514.json"), &reverted)
	const revertedTxID = "9e542921f948f21189249954baacde340f4892259336ec5201fbb94fe1ac8c29"
	node.txInfos[revertedTxID] = string(reverted[0])

	block, err := adapter.GetTransactionBlock(context.Background(), "0x"+tronUSDTDepositTxID)
	if err != nil || block != tronBlockWithUSDT {
		t.Fatalf("included: %d, %v", block, err)
	}
	block, err = adapter.GetTransactionBlock(context.Background(), "ab"+tronUSDTDepositTxID[2:])
	if err != nil || block != 0 {
		t.Fatalf("unknown: %d, %v", block, err)
	}
	if _, err := adapter.GetTransactionBlock(context.Background(), revertedTxID); err == nil {
		t.Fatal("reverted transaction reported as included")
	}
	if _, err := adapter.GetTransactionBlock(context.Background(), "abc"); err == nil {
		t.Fatal("malformed txid accepted")
	}
	if err := adapter.AwaitTransactionIncluded(context.Background(), tronUSDTDepositTxID); err != nil {
		t.Fatal(err)
	}
}
