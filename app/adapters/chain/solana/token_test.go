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

const solanaBalanceOwner = "7EcDhSYGxXyscszYEp35KHN8vvw3svAuLKTzXwCFLtV"

func TestSolanaGetTokenBalance(t *testing.T) {
	ownerKey := solana.MustPublicKeyFromBase58(solanaBalanceOwner)
	mintKey := solana.MustPublicKeyFromBase58(models.USDCMintSOL)
	ata, _, err := solana.FindAssociatedTokenAddress(ownerKey, mintKey)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "getTokenAccountBalance" {
			t.Fatalf("method %s", req.Method)
		}
		if len(req.Params) == 0 {
			t.Fatal("missing params")
		}
		var gotATA string
		if err := json.Unmarshal(req.Params[0], &gotATA); err != nil {
			t.Fatal(err)
		}
		if gotATA != ata.String() {
			t.Fatalf("ata %s want %s", gotATA, ata.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"amount":"1500000","decimals":6,"uiAmount":1.5,"uiAmountString":"1.5"}}}`)
	}))
	defer srv.Close()

	live := NewSolanaLive(SolanaConfig{
		ChainIDStr:   models.ChainSOL,
		NativeSymbol: models.NativeSOL,
		RPCURL:       srv.URL,
	})
	bal, err := live.GetTokenBalance(context.Background(), solanaBalanceOwner, types.Token{
		Symbol:   models.SymbolUSDC,
		Contract: models.USDCMintSOL,
		Decimals: 6,
		ChainID:  models.ChainSOL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bal.Amount.Cmp(big.NewInt(1_500_000)) != 0 || bal.Asset != models.SymbolUSDC || bal.Decimals != 6 {
		t.Fatalf("%+v", bal)
	}
}

func TestSolanaGetTokenBalance_MissingAccountIsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"could not find account"}}`)
	}))
	defer srv.Close()

	live := NewSolanaLive(SolanaConfig{
		ChainIDStr:   models.ChainSOL,
		NativeSymbol: models.NativeSOL,
		RPCURL:       srv.URL,
	})
	bal, err := live.GetTokenBalance(context.Background(), solanaBalanceOwner, types.Token{
		Symbol:   models.SymbolUSDC,
		Contract: models.USDCMintSOL,
		Decimals: 6,
		ChainID:  models.ChainSOL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bal.Amount.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("%+v", bal)
	}
}
