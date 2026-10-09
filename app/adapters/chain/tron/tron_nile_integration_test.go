//go:build tron_nile

package tron

// Read-only comparison against Nile (go test -tags tron_nile -run TestTronNile
// ./app/services/chain/): transactions built locally must equal, byte for byte, the
// raw_data the node builds for the same transfer, except for the reference block,
// expiration and timestamp, which each side takes from its own clock and head.
// Nothing is signed or broadcast.

import (
	"bytes"
	"context"
	"encoding/hex"
	"math/big"
	"os"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	tronNileURL = "https://nile.trongrid.io"
	// Activated Nile accounts: private key 1's address, and the sender of the USDT
	// transfer c4c6e63a… (holds USDT); the recipient of that transfer.
	tronNileTRXOwner   = "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"
	tronNileUSDTOwner  = "THwaqc496gjyFLqrvVeUNTCWY7Vn9RH995"
	tronNileRecipient  = "TL1eeYCiqPwERsRVdscUBEWi5aHvnwtvfh"
	tronNileTRXAmount  = 1_000_000
	tronNileUSDTAmount = 1
)

func newTronNileAdapter() *TronLive {
	return NewTronLive(TronConfig{ChainIDStr: models.ChainTron, ChainName: "TRON Nile", NativeSymbol: models.NativeTRX,
		RPCURL: tronNileURL, IsTestnet: true, Confirmations: 20, Tokens: []types.Token{tronTestNileUSDT}})
}

// requireSameExceptReference re-encodes the node's raw data with the local
// reference block, expiration and timestamp and requires the local bytes.
func requireSameExceptReference(t *testing.T, local *types.UnsignedTx, nodeRawHex string) {
	t.Helper()
	localRaw, err := decodeTronRawData(mustHex(t, local.Metadata["raw_data_hex"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	nodeRaw, err := decodeTronRawData(mustHex(t, nodeRawHex))
	if err != nil {
		t.Fatalf("node raw data is not in the form this adapter builds: %v", err)
	}
	t.Logf("node  ref=%x/%x expiration=%d timestamp=%d fee_limit=%d", nodeRaw.refBlockBytes, nodeRaw.refBlockHash, nodeRaw.expiration, nodeRaw.timestamp, nodeRaw.feeLimit)
	t.Logf("local ref=%x/%x expiration=%d timestamp=%d fee_limit=%d", localRaw.refBlockBytes, localRaw.refBlockHash, localRaw.expiration, localRaw.timestamp, localRaw.feeLimit)
	nodeRaw.refBlockBytes, nodeRaw.refBlockHash = localRaw.refBlockBytes, localRaw.refBlockHash
	nodeRaw.expiration, nodeRaw.timestamp = localRaw.expiration, localRaw.timestamp
	aligned, err := nodeRaw.encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aligned, mustHex(t, local.Metadata["raw_data_hex"].(string))) {
		t.Fatalf("raw data differs\nlocal %s\nnode  %x", local.Metadata["raw_data_hex"], aligned)
	}
}

func TestTronNileTRXTransferMatchesCreateTransaction(t *testing.T) {
	adapter := newTronNileAdapter()
	ctx := context.Background()
	local, err := adapter.BuildTransfer(ctx, types.TransferRequest{From: tronNileTRXOwner, To: tronNileRecipient, Amount: big.NewInt(tronNileTRXAmount), Asset: models.NativeTRX})
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		RawDataHex string `json:"raw_data_hex"`
	}
	if err := adapter.post(ctx, "/wallet/createtransaction", map[string]any{
		"owner_address": mustTronHex(t, tronNileTRXOwner), "to_address": mustTronHex(t, tronNileRecipient), "amount": tronNileTRXAmount,
	}, &node); err != nil {
		t.Fatal(err)
	}
	requireSameExceptReference(t, local, node.RawDataHex)
	t.Logf("TRX fee quoted %v sun", local.Metadata["fee"])
}

func TestTronNileTRC20TransferMatchesTriggerSmartContract(t *testing.T) {
	adapter := newTronNileAdapter()
	ctx := context.Background()
	token := tronTestNileUSDT
	local, err := adapter.BuildTransfer(ctx, types.TransferRequest{From: tronNileUSDTOwner, To: tronNileRecipient, Amount: big.NewInt(tronNileUSDTAmount), Asset: token.Symbol, Token: &token})
	if err != nil {
		t.Fatal(err)
	}
	recipient := mustHex(t, mustTronHex(t, tronNileRecipient))
	data, err := encodeTRC20Transfer(recipient, big.NewInt(tronNileUSDTAmount))
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		Result      tronCallResult `json:"result"`
		Transaction struct {
			RawDataHex string `json:"raw_data_hex"`
		} `json:"transaction"`
	}
	if err := adapter.post(ctx, "/wallet/triggersmartcontract", map[string]any{
		"owner_address": mustTronHex(t, tronNileUSDTOwner), "contract_address": mustTronHex(t, token.Contract),
		"function_selector": tronTRC20TransferSelector, "parameter": hex.EncodeToString(data[len(tronTRC20TransferMethodID):]),
		"fee_limit": local.Metadata["fee_limit"], "call_value": 0,
	}, &node); err != nil {
		t.Fatal(err)
	}
	if !node.Result.Result {
		t.Fatalf("triggersmartcontract: %s", node.Result.describe())
	}
	requireSameExceptReference(t, local, node.Transaction.RawDataHex)
	t.Logf("USDT fee quoted %v sun (fee_limit %v)", local.Metadata["fee"], local.Metadata["fee_limit"])
}

// TronGrid mainnet has no /wallet/estimateenergy: USDT energy comes from
// triggerconstantcontract's energy_used. TRON_MAINNET_USDT_HOLDER and
// TRON_MAINNET_USDT_RECIPIENT (41… hex) pick a current holder and an existing
// holder; the defaults are the sender and recipient of the fixture transfer
// 0b07474f…, which may have emptied since (the quote then uses the reference).
func TestTronNileMainnetUSDTQuoteFallsBack(t *testing.T) {
	adapter := NewTronLive(TronConfig{ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, RPCURL: "https://api.trongrid.io"})
	token := types.Token{Symbol: models.SymbolUSDT, Contract: models.USDTContractTron, Decimals: 6, ChainID: models.ChainTron}
	holder, err := addressing.TronAddressFromHex(envOr("TRON_MAINNET_USDT_HOLDER", "4193180b62ef4bd4b57896c79d2a1d3bd91e599d35"))
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := addressing.TronAddressFromHex(envOr("TRON_MAINNET_USDT_RECIPIENT", "4130760c7e10b1d3509d8d64a7e9eb9ab94bc83495"))
	if err != nil {
		t.Fatal(err)
	}
	for name, to := range map[string]string{"existing holder": recipient, "new holder": chain.TronFeeProbeRecipient()} {
		quote, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{From: holder, To: to, Amount: big.NewInt(1), Token: &token})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("mainnet USDT to %-15s fee=%v sun (%s)", name, quote.Fee(), describeQuote(quote))
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func TestTronNileQuotesToday(t *testing.T) {
	adapter := newTronNileAdapter()
	ctx := context.Background()
	token := tronTestNileUSDT
	for name, req := range map[string]types.TransferRequest{
		"TRX to an existing account": {From: tronNileTRXOwner, To: tronNileRecipient, Amount: big.NewInt(tronNileTRXAmount)},
		"TRX to a new account":       {From: tronNileTRXOwner, To: chain.TronFeeProbeRecipient(), Amount: big.NewInt(tronNileTRXAmount)},
		"USDT to an existing holder": {From: tronNileUSDTOwner, To: tronNileRecipient, Amount: big.NewInt(tronNileUSDTAmount), Token: &token},
		"USDT to a new holder":       {From: tronNileUSDTOwner, To: chain.TronFeeProbeRecipient(), Amount: big.NewInt(tronNileUSDTAmount), Token: &token},
		"USDT from a sender without": {From: chain.TronFeeProbeRecipient(), To: tronNileRecipient, Amount: big.NewInt(tronNileUSDTAmount), Token: &token},
	} {
		quote, err := adapter.QuoteTransferFee(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%-28s fee=%v sun (%s)", name, quote.Fee(), describeQuote(quote))
	}
}
