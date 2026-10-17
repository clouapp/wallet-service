package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const devnetUSDCMint = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"

// splBlockNode is a Solana node whose every block holds one transaction that moves
// 2200 base units of devnet USDC from "seller" to "buyer".
func splBlockNode(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"blockhash":"hash","blockTime":1,"transactions":[{
			"transaction":{"signatures":["sig"],"accountKeys":[{"pubkey":"payer","signer":true},{"pubkey":"ataSeller"},{"pubkey":"ataBuyer"}]},
			"meta":{"err":null,"preBalances":[10,2039280,2039280],"postBalances":[5,2039280,2039280],
				"preTokenBalances":[{"accountIndex":1,"mint":"`+devnetUSDCMint+`","owner":"seller","uiTokenAmount":{"amount":"5000","decimals":6}},
					{"accountIndex":2,"mint":"`+devnetUSDCMint+`","owner":"buyer","uiTokenAmount":{"amount":"0","decimals":6}}],
				"postTokenBalances":[{"accountIndex":1,"mint":"`+devnetUSDCMint+`","owner":"seller","uiTokenAmount":{"amount":"2800","decimals":6}},
					{"accountIndex":2,"mint":"`+devnetUSDCMint+`","owner":"buyer","uiTokenAmount":{"amount":"2200","decimals":6}}]}}]}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRegister_ActiveChains_SolanaScansTheRegisteredSPLTokens(t *testing.T) {
	node := splBlockNode(t)
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(string) (string, error) { return node.URL, nil }

	rows := []models.Chain{{ID: models.ChainSOL, Name: "Solana", AdapterType: models.AdapterTypeSolana, NativeSymbol: models.NativeSOL, NativeDecimals: 9, IsTestnet: true, RequiredConfirmations: 1, RpcURL: "sealed"}}
	reg := chainpkg.NewRegistry()
	registerActiveChains(reg, rows, map[string][]types.Token{
		models.ChainSOL: {{Symbol: models.SymbolUSDC, Contract: devnetUSDCMint, Decimals: 6, ChainID: models.ChainSOL}},
	})

	sol, err := reg.Chain(models.ChainSOL)
	if err != nil {
		t.Fatal(err)
	}
	transfers, err := sol.ScanBlock(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 1 || transfers[0].Token == nil || transfers[0].Token.Contract != devnetUSDCMint ||
		transfers[0].To != "buyer" || transfers[0].From != "seller" || transfers[0].Amount.Int64() != 2200 {
		t.Fatalf("the sol adapter must report the registered USDC credit, got %+v", transfers)
	}
}
