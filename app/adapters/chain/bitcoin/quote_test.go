package bitcoin

import (
	"context"
	"math/big"
	"net/http"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	quoteFeeEstimatesPath = esploraTestPrefix + "/fee-estimates"
	quoteUTXOPath         = esploraTestPrefix + "/address/" + feeTestFrom + "/utxo"
)

func quoteEsplora(t *testing.T, utxos []btcInput) *BitcoinLive {
	t.Helper()
	esplora := newFakeEsplora(t)
	esplora.ok(quoteFeeEstimatesPath, liveTestnet4FeeEstimates)
	esplora.ok(quoteUTXOPath, utxoJSON(utxos, true))
	return esplora.adapter(nil)
}

// The 3-block testnet4 estimate (0.5 sat/vB) is floored at 1 sat/vB, so a one-input
// payment with change costs its 141 vB: the fee of the f12f9d93… withdrawal.
func TestBitcoin_QuoteTransferFee_OneInputWithChangeAtTheFlooredRate(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 337_841)})

	quote, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := chain.BitcoinFeeQuote{Fee: 141, Inputs: 1, Outputs: 2, VSize: 141, MilliSatPerVByte: 1_000, Covered: true}
	if quote != want {
		t.Fatalf("quote %+v, want %+v", quote, want)
	}
	if fee := adapter.estimateTransferFeeSats(context.Background(), types.TransferRequest{From: feeTestFrom, Amount: big.NewInt(10_000)}); fee != quote.Fee {
		t.Fatalf("EstimateFee path %d disagrees with the quote %d", fee, quote.Fee)
	}
}

func TestBitcoin_QuoteTransferFee_UTXOFailureIsAnErrorNotATypicalFee(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(quoteFeeEstimatesPath, liveTestnet4FeeEstimates)
	esplora.on(quoteUTXOPath, esploraAnswer{http.StatusInternalServerError, "boom"})

	if _, err := esplora.adapter(nil).QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil); err == nil {
		t.Fatal("a failed UTXO listing must fail the quote")
	}
}

func TestBitcoin_QuoteTransferFee_UncoveredAmountQuotesATypicalTransfer(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 5_000)})

	quote, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Covered || quote.Fee != 141 || quote.Inputs != 1 || quote.Outputs != 2 {
		t.Fatalf("quote %+v, want the uncovered typical 1-in/2-out transfer", quote)
	}
}

func TestBitcoin_QuoteTransferFee_PendingInputsJoinTheSelection(t *testing.T) {
	adapter := quoteEsplora(t, nil)

	quote, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), []*big.Int{big.NewInt(50_000)})
	if err != nil {
		t.Fatal(err)
	}
	if !quote.Covered || quote.Inputs != 1 || quote.Fee != 141 {
		t.Fatalf("quote %+v", quote)
	}
	if _, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), []*big.Int{big.NewInt(-1)}); err == nil {
		t.Fatal("a negative pending input must be refused")
	}
}

func TestBitcoin_QuoteTransferFee_EstimatorFailureReportsTheFlatFeeTheBuilderUses(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(quoteUTXOPath, utxoJSON([]btcInput{utxo(0, 100_000)}, true))

	quote, err := esplora.adapter(nil).QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !quote.FlatFallback || quote.Fee != flatTestFee || quote.MilliSatPerVByte != 0 {
		t.Fatalf("quote %+v, want the flagged flat fee %d", quote, flatTestFee)
	}
}

func TestBitcoin_QuoteTransferFee_ValidatesItsInputs(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 100_000)})
	for name, call := range map[string]func() error{
		"empty from": func() error {
			_, err := adapter.QuoteTransferFee(context.Background(), " ", big.NewInt(1_000), nil)
			return err
		},
		"nil amount": func() error {
			_, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, nil, nil)
			return err
		},
		"zero amount": func() error {
			_, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(0), nil)
			return err
		},
		"negative": func() error {
			_, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(-5), nil)
			return err
		},
		"sweep no from": func() error { _, err := adapter.QuoteSweepFee(context.Background(), ""); return err },
	} {
		if call() == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestBitcoin_QuoteSweepFee_SpendsEveryInputToOneOutput(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 40_000), utxo(1, 40_000)})

	quote, err := adapter.QuoteSweepFee(context.Background(), feeTestFrom)
	if err != nil {
		t.Fatal(err)
	}
	if !quote.Covered || quote.Inputs != 2 || quote.Outputs != 1 || quote.VSize != 178 || quote.Fee != 178 {
		t.Fatalf("quote %+v, want 2 inputs → 1 output, 178 vB at 1 sat/vB", quote)
	}
}
