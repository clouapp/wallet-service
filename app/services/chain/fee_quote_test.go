package chain

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	quoteFeeEstimatesPath = esploraTestPrefix + "/fee-estimates"
	quoteUTXOPath         = esploraTestPrefix + "/address/" + feeTestFrom + "/utxo"
	// splTokenAccountRentLamports is devnet's rent-exempt minimum for 165 bytes.
	splTokenAccountRentLamports = 2_039_280
	systemAccountRentLamports   = 890_880
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
func TestBitcoinQuoteTransferFee_OneInputWithChangeAtTheFlooredRate(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 337_841)})

	quote, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := BitcoinFeeQuote{Fee: 141, Inputs: 1, Outputs: 2, VSize: 141, MilliSatPerVByte: 1_000, Covered: true}
	if quote != want {
		t.Fatalf("quote %+v, want %+v", quote, want)
	}
	if fee := adapter.estimateTransferFeeSats(context.Background(), types.TransferRequest{From: feeTestFrom, Amount: big.NewInt(10_000)}); fee != quote.Fee {
		t.Fatalf("EstimateFee path %d disagrees with the quote %d", fee, quote.Fee)
	}
}

func TestBitcoinQuoteTransferFee_UTXOFailureIsAnErrorNotATypicalFee(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(quoteFeeEstimatesPath, liveTestnet4FeeEstimates)
	esplora.on(quoteUTXOPath, esploraAnswer{http.StatusInternalServerError, "boom"})

	if _, err := esplora.adapter(nil).QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil); err == nil {
		t.Fatal("a failed UTXO listing must fail the quote")
	}
}

func TestBitcoinQuoteTransferFee_UncoveredAmountQuotesATypicalTransfer(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 5_000)})

	quote, err := adapter.QuoteTransferFee(context.Background(), feeTestFrom, big.NewInt(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Covered || quote.Fee != 141 || quote.Inputs != 1 || quote.Outputs != 2 {
		t.Fatalf("quote %+v, want the uncovered typical 1-in/2-out transfer", quote)
	}
}

func TestBitcoinQuoteTransferFee_PendingInputsJoinTheSelection(t *testing.T) {
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

func TestBitcoinQuoteTransferFee_EstimatorFailureReportsTheFlatFeeTheBuilderUses(t *testing.T) {
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

func TestBitcoinQuoteTransferFee_ValidatesItsInputs(t *testing.T) {
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

func TestBitcoinQuoteSweepFee_SpendsEveryInputToOneOutput(t *testing.T) {
	adapter := quoteEsplora(t, []btcInput{utxo(0, 40_000), utxo(1, 40_000)})

	quote, err := adapter.QuoteSweepFee(context.Background(), feeTestFrom)
	if err != nil {
		t.Fatal(err)
	}
	if !quote.Covered || quote.Inputs != 2 || quote.Outputs != 1 || quote.VSize != 178 || quote.Fee != 178 {
		t.Fatalf("quote %+v, want 2 inputs → 1 output, 178 vB at 1 sat/vB", quote)
	}
}

// fakeSolanaRPC answers JSON-RPC methods with canned results; unknown methods fail the test.
func fakeSolanaRPC(t *testing.T, results map[string]string) *SolanaLive {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		key := req.Method
		if req.Method == "getMinimumBalanceForRentExemption" && len(req.Params) > 0 {
			key += ":" + string(req.Params[0])
		}
		result, ok := results[key]
		if !ok {
			t.Errorf("unexpected solana rpc %s", key)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	return NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL})
}

var quoteUSDC = &types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCMintSOL, Decimals: 6}

func TestSolanaQuoteTransferFee_NativePaysOneSignature(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{})

	quote, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{From: solanaBalanceOwner, To: solanaBalanceOwner})
	if err != nil {
		t.Fatal(err)
	}
	if quote.Fee().Int64() != solanaNativeFeeLamports || quote.Signatures != 1 {
		t.Fatalf("quote %+v", quote)
	}
}

func TestSolanaQuoteTransferFee_MissingTokenAccountAddsItsRent(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{
		"getAccountInfo":                        `"result":{"context":{"slot":1},"value":null}`,
		"getMinimumBalanceForRentExemption:165": `"result":2039280`,
	})

	quote, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{To: solanaBalanceOwner, Token: quoteUSDC})
	if err != nil {
		t.Fatal(err)
	}
	if quote.AccountCreationLamports.Int64() != splTokenAccountRentLamports || quote.Fee().Int64() != solanaNativeFeeLamports+splTokenAccountRentLamports {
		t.Fatalf("quote %+v fee %s", quote, quote.Fee())
	}
}

func TestSolanaQuoteTransferFee_ExistingTokenAccountPaysOnlyTheSignature(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{
		"getAccountInfo": `"result":{"context":{"slot":1},"value":{"lamports":2039280}}`,
	})

	quote, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{To: solanaBalanceOwner, Token: quoteUSDC})
	if err != nil {
		t.Fatal(err)
	}
	if quote.Fee().Int64() != solanaNativeFeeLamports {
		t.Fatalf("fee %s", quote.Fee())
	}
}

func TestSolanaQuoteTransferFee_RentFailureIsAnError(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{
		"getAccountInfo":                        `"result":{"context":{"slot":1},"value":null}`,
		"getMinimumBalanceForRentExemption:165": `"error":{"code":-32000,"message":"node behind"}`,
	})

	if _, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{To: solanaBalanceOwner, Token: quoteUSDC}); err == nil {
		t.Fatal("a failed rent lookup must fail the quote")
	}
}

func TestSolanaNativeTransferReserveReadsTheSystemAccountRent(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{"getMinimumBalanceForRentExemption:0": `"result":890880`})

	fee, minimum, err := adapter.NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fee.Int64() != solanaNativeFeeLamports || minimum.Int64() != systemAccountRentLamports {
		t.Fatalf("fee %s minimum %s", fee, minimum)
	}
}

// Solana answers getAccountInfo for a missing account with value null, not an error:
// the transfer must then create the recipient's token account.
func TestSolanaBuildTransferCreatesTheTokenAccountWhenGetAccountInfoIsNull(t *testing.T) {
	hash := solana.Hash{}
	hash[0] = 1
	adapter := fakeSolanaRPC(t, map[string]string{
		"getAccountInfo":     `"result":{"context":{"slot":1},"value":null}`,
		"getLatestBlockhash": `"result":{"value":{"blockhash":"` + hash.String() + `"}}`,
	})

	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: solanaBalanceOwner, To: solanaBalanceOwner, Amount: big.NewInt(1_500_000), Token: quoteUSDC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsigned.Metadata["dest_ata_exists"] != false {
		t.Fatalf("metadata %+v: a null account must be created", unsigned.Metadata)
	}
}
