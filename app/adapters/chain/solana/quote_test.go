package solana

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
	// splTokenAccountRentLamports is devnet's rent-exempt minimum for 165 bytes.
	splTokenAccountRentLamports = 2_039_280
	systemAccountRentLamports   = 890_880
)

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

func TestSolana_QuoteTransferFee_NativePaysOneSignature(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{})

	quote, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{From: solanaBalanceOwner, To: solanaBalanceOwner})
	if err != nil {
		t.Fatal(err)
	}
	if quote.Fee().Int64() != solanaNativeFeeLamports || quote.Signatures != 1 {
		t.Fatalf("quote %+v", quote)
	}
}

func TestSolana_QuoteTransferFee_MissingTokenAccountAddsItsRent(t *testing.T) {
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

func TestSolana_QuoteTransferFee_ExistingTokenAccountPaysOnlyTheSignature(t *testing.T) {
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

func TestSolana_QuoteTransferFee_RentFailureIsAnError(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{
		"getAccountInfo":                        `"result":{"context":{"slot":1},"value":null}`,
		"getMinimumBalanceForRentExemption:165": `"error":{"code":-32000,"message":"node behind"}`,
	})

	if _, err := adapter.QuoteTransferFee(context.Background(), types.TransferRequest{To: solanaBalanceOwner, Token: quoteUSDC}); err == nil {
		t.Fatal("a failed rent lookup must fail the quote")
	}
}

func TestSolana_Native_TransferReserveReadsTheSystemAccountRent(t *testing.T) {
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
func TestSolana_Build_TransferCreatesTheTokenAccountWhenGetAccountInfoIsNull(t *testing.T) {
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
