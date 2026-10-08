//go:build ltc_fallback_live

package bitcoin

// Read-only failover check on Litecoin mainnet (go test -tags ltc_fallback_live -run
// TestLitecoinMainnetFallbackLive -v ./app/services/chain/): the primary points at a
// closed local port, so every read comes from the built-in mainnet fallbacks, and
// each fallback is then asked on its own. Nothing is signed or broadcast. The Tatum
// part stays within its keyless 5 requests a minute.

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const (
	ltcMainnetLiveUnreachablePrimary = "http://127.0.0.1:1/api"

	// A public exchange address (bitinfocharts / coincarp rich lists) whose output
	// b331c618…:0 (606,636.48487183 LTC, block 3125684) has stayed unspent.
	ltcMainnetLiveAddress  = "ltc1qw5r2tx3e2ff4eurmmpz43h85m2fkyv5eta3l5h"
	ltcMainnetLiveTx       = "b331c618480a1727e0cf29b105315637ab32b18407944215ef3994ccf70f0046"
	ltcMainnetLiveTxBlock  = 3125684
	ltcMainnetLiveTxOutSat = 60663648487183
)

func TestLitecoinMainnetFallbackLive(t *testing.T) {
	cfg := BitcoinConfig{ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC, RPCURL: ltcMainnetLiveUnreachablePrimary}
	for _, rawURL := range DefaultBitcoinFallbackURLs(cfg) {
		cfg.Fallbacks = append(cfg.Fallbacks, BitcoinFallback{URL: rawURL})
	}
	live := NewBitcoinLive(cfg)
	if got, want := len(live.providers.members), len(cfg.Fallbacks)+1; got != want {
		t.Fatalf("providers = %d, want %d", got, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	utxos, err := live.listConfirmedUTXOs(ctx, ltcMainnetLiveAddress)
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	found := false
	for _, utxo := range utxos {
		found = found || (utxo.TxID == ltcMainnetLiveTx && utxo.Vout == 0 && utxo.Value == ltcMainnetLiveTxOutSat)
	}
	if !found {
		t.Fatalf("output %s:0 not among the %d UTXOs", ltcMainnetLiveTx, len(utxos))
	}
	balance, err := live.GetBalance(ctx, ltcMainnetLiveAddress)
	if err != nil || balance.Amount.Int64() < ltcMainnetLiveTxOutSat {
		t.Fatalf("balance = %+v, %v", balance, err)
	}
	tip, err := live.GetLatestBlock(ctx)
	if err != nil || tip <= ltcMainnetLiveTxBlock {
		t.Fatalf("tip = %d, %v", tip, err)
	}
	height, err := live.GetTransactionBlock(ctx, ltcMainnetLiveTx)
	if err != nil || height != ltcMainnetLiveTxBlock {
		t.Fatalf("tx block = %d, %v; want %d", height, err, ltcMainnetLiveTxBlock)
	}
	rate, err := live.fetchFeeRate(ctx)
	if err != nil {
		t.Fatalf("fee rate: %v", err)
	}
	t.Logf("failover: %d UTXOs, balance %s LTC, tip %d, tx block %d, fee %d milli-sat/vB", len(utxos), balance.Human, tip, height, rate)

	for _, member := range live.providers.members[1:] {
		provider := member.provider
		tip, tipErr := provider.latestBlock(ctx)
		rate, rateErr := provider.feeRate(ctx)
		if tipErr != nil || rateErr != nil {
			t.Errorf("%s: tip %d (%v), fee %d (%v)", provider.label(), tip, tipErr, rate, rateErr)
			continue
		}
		if _, isTatum := provider.(tatumProvider); isTatum {
			t.Logf("%s: genesis ok, tip %d, fee %d milli-sat/vB (keyless: no UTXO lookup)", provider.label(), tip, rate)
			continue
		}
		balance, err := provider.balance(ctx, ltcMainnetLiveAddress)
		if err != nil {
			t.Errorf("%s: balance: %v", provider.label(), err)
			continue
		}
		t.Logf("%s: genesis ok, tip %d, fee %d milli-sat/vB, balance %s LTC", provider.label(), tip, rate, balance.Human)
	}
}

// TestLitecoinMainnetTatumLive asks the keyless Tatum gateway alone for a block scan
// (no Electrum server lists a block) and a transaction's block: 4 requests.
func TestLitecoinMainnetTatumLive(t *testing.T) {
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC, RPCURL: ltcMainnetLiveUnreachablePrimary,
		Fallbacks: []BitcoinFallback{{URL: ltcMainnetTatumGateway}},
	})
	tatum := live.providers.members[1].provider
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	height, err := tatum.transactionBlock(ctx, ltcMainnetLiveTx)
	if err != nil || height != ltcMainnetLiveTxBlock {
		t.Fatalf("%s: tx block = %d, %v; want %d", tatum.label(), height, err, ltcMainnetLiveTxBlock)
	}
	transfers, err := tatum.scanBlock(ctx, ltcMainnetLiveTxBlock)
	if err != nil {
		t.Fatalf("%s: scan block %d: %v", tatum.label(), ltcMainnetLiveTxBlock, err)
	}
	paid := false
	for _, transfer := range transfers {
		paid = paid || (transfer.TxHash == ltcMainnetLiveTx && transfer.To == ltcMainnetLiveAddress && transfer.Amount.Int64() == ltcMainnetLiveTxOutSat)
	}
	if !paid {
		t.Fatalf("block %d scan (%d transfers) misses %s:0", ltcMainnetLiveTxBlock, len(transfers), ltcMainnetLiveTx)
	}
	t.Logf("%s: tx %s in block %d; block scan %d transfers, includes it", tatum.label(), ltcMainnetLiveTx[:8], height, len(transfers))
}
