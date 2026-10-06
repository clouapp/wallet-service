package bitcoin

import (
	"context"
	"math/big"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

func TestBitcoin_Rate_FollowsTheWalletPolicyAndKeepsTheNetworkRateCached(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(esploraTestPrefix+"/fee-estimates", `{"3":4.2}`)
	shared := feeTestAdapter(esplora)
	policy, err := chain.NewFeePolicy(chain.FeePolicyDeps{Multiplier: decimal.RequireFromString("1.5")})
	if err != nil {
		t.Fatal(err)
	}
	scoped := shared.WithFeePolicy(policy).(*BitcoinLive)

	if got := scoped.feePolicy(context.Background()).milliSatPerVByte; got != 6_300 {
		t.Fatalf("scoped rate %d, want 4200 × 1.5 = 6300", got)
	}
	if got := shared.feePolicy(context.Background()).milliSatPerVByte; got != 4_200 {
		t.Fatalf("shared rate %d, want the network's 4200", got)
	}
	if hits := esplora.hitCount(esploraTestPrefix + "/fee-estimates"); hits != 1 {
		t.Fatalf("the scoped and shared adapters must share the rate cache, fetched %d times", hits)
	}

	esplora.ok(utxoPath(), utxoJSON([]btcInput{utxo(0, 40_000), utxo(1, 40_000)}, true))
	req := types.TransferRequest{From: feeTestFrom, To: feeTestTo, Amount: big.NewInt(60_000)}
	quote, err := scoped.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(60_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := scoped.BuildTransfer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if built := unsigned.Metadata["fee"].(int64); built != quote.Fee || quote.MilliSatPerVByte != 6_300 {
		t.Fatalf("quote %+v, built fee %d", quote, built)
	}
}

func TestBitcoin_Flat_FallbackFollowsTheWalletPolicy(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.on(esploraTestPrefix+"/fee-estimates", esploraAnswer{http.StatusInternalServerError, "boom"})
	policy, err := chain.NewFeePolicy(chain.FeePolicyDeps{Multiplier: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	scoped := feeTestAdapter(esplora).WithFeePolicy(policy).(*BitcoinLive)
	got := scoped.feePolicy(context.Background())
	if got.milliSatPerVByte != 0 || got.flatFee != int64(btcFeeVBytes*btcDefaultFeeRate*2) {
		t.Fatalf("flat fallback %+v, want %d sats", got, btcFeeVBytes*btcDefaultFeeRate*2)
	}
}
