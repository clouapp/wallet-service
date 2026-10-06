//go:build ltc_fallback_live

package bitcoin

// Read-only failover check on Litecoin testnet (go test -tags ltc_fallback_live -run
// TestLitecoinFallbackLive -v ./app/services/chain/): the primary points at a closed
// local port, so every read must come from the public fallbacks. Nothing is signed or
// broadcast. Tatum's keyless gateway allows 5 requests a minute, which the block scan
// below stays within.

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const (
	ltcLiveUnreachablePrimary = "http://127.0.0.1:1/testnet/api"
	ltcLiveElectrumBysh       = "electrum+ssl://electrum-ltc.bysh.me:51002?cert_sha256=fdf3c121181c14100d8740007ed546de388099c7090315ca2cf995bb4620c41d"
	ltcLiveElectrumXurious    = "electrum+ssl://electrum.ltc.xurious.com:51002?cert_sha256=e3aedd3093856098e2efe66d96fb7cd97adae8eeda1d070d460e84dc5134cf26"
	ltcLiveTatumGateway       = "https://litecoin-testnet.gateway.tatum.io"

	// ltc_withdraw base address and the e2e withdrawal 8ab2ea86… (block 4907446) whose
	// change output it holds.
	ltcLiveAddress    = "tltc1q4nhl6f48sz3hqjcknptjdh7hgkt253ul6kq4dx"
	ltcLiveTx         = "8ab2ea8689c293e4cb441752263765fc27b56fd414d55c6d7f994866a7dba19d"
	ltcLiveTxBlock    = 4907446
	ltcLiveTxOutValue = 49859
)

func TestLitecoinFallbackLive(t *testing.T) {
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainTLTC, NativeSymbol: models.NativeLTC, IsTestnet: true,
		RPCURL: ltcLiveUnreachablePrimary,
		Fallbacks: []BitcoinFallback{
			{URL: ltcLiveElectrumBysh}, {URL: ltcLiveElectrumXurious}, {URL: ltcLiveTatumGateway},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	utxos, err := live.listConfirmedUTXOs(ctx, ltcLiveAddress)
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	found := false
	for _, utxo := range utxos {
		found = found || (utxo.TxID == ltcLiveTx && utxo.Value == ltcLiveTxOutValue)
	}
	t.Logf("utxos of %s: %+v", ltcLiveAddress, utxos)
	if !found {
		t.Fatalf("change output of %s not among the UTXOs", ltcLiveTx)
	}
	balance, err := live.GetBalance(ctx, ltcLiveAddress)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	t.Logf("balance %s LTC", balance.Human)
	tip, err := live.GetLatestBlock(ctx)
	if err != nil || tip <= ltcLiveTxBlock {
		t.Fatalf("tip = %d, %v", tip, err)
	}
	height, err := live.GetTransactionBlock(ctx, ltcLiveTx)
	if err != nil || height != ltcLiveTxBlock {
		t.Fatalf("tx block = %d, %v; want %d", height, err, ltcLiveTxBlock)
	}
	rate, err := live.fetchFeeRate(ctx)
	if err != nil {
		t.Fatalf("fee rate: %v", err)
	}
	t.Logf("tip %d, tx block %d (%d confirmations), fee %d milli-sat/vB", tip, height, tip-height+1, rate)

	transfers, err := live.ScanBlock(ctx, ltcLiveTxBlock)
	if err != nil {
		t.Fatalf("scan block via the json-rpc fallback: %v", err)
	}
	paid := false
	for _, transfer := range transfers {
		paid = paid || (transfer.TxHash == ltcLiveTx && transfer.To == ltcLiveAddress && transfer.Amount.Int64() == ltcLiveTxOutValue)
	}
	if !paid {
		t.Fatalf("block %d scan (%d transfers) misses %s", ltcLiveTxBlock, len(transfers), ltcLiveTx)
	}
	t.Logf("block %d: %d transfers, includes the change output", ltcLiveTxBlock, len(transfers))
}
