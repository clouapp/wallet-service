package evm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// The scan reports, for every transfer, the account that signed its transaction
// and the nonce it spent: the tracker proves with them that a deposit whose block
// was reorganized away can never land. A token transfer carries its transaction's
// signer, not the token sender of its Transfer log.
func TestEVM_ScanBlock_ReportsTheSignerAndNonceOfEachTransfer(t *testing.T) {
	const (
		signer    = "0x00000000000000000000000000000000000000f1"
		router    = "0x00000000000000000000000000000000000000f2"
		recipient = "0x00000000000000000000000000000000000000c1"
		usdc      = "0x1c7d4b196cb0c7b01d743fbc6116a902379c7238"
		nativeTx  = "0x00000000000000000000000000000000000000000000000000000000000000a1"
		tokenTx   = "0x00000000000000000000000000000000000000000000000000000000000000a2"
	)
	block := `{"jsonrpc":"2.0","id":1,"result":{"hash":"0xb1","timestamp":"0x1","logsBloom":"0x` + strings.Repeat("ff", 256) + `","transactions":[` +
		`{"hash":"` + nativeTx + `","from":"` + signer + `","to":"` + recipient + `","value":"0x10","nonce":"0x7"},` +
		`{"hash":"` + tokenTx + `","from":"` + signer + `","to":"` + usdc + `","value":"0x0","nonce":"0x8"}]}}`
	logs := `{"jsonrpc":"2.0","id":1,"result":[{"address":"` + usdc + `","blockHash":"0xb1","transactionHash":"` + tokenTx + `","logIndex":"0x0","data":"0x05",` +
		`"topics":["` + erc20TransferTopic + `","0x000000000000000000000000` + strings.TrimPrefix(router, "0x") + `","0x000000000000000000000000` + strings.TrimPrefix(recipient, "0x") + `"]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "eth_getLogs" {
			_, _ = io.WriteString(w, logs)
			return
		}
		_, _ = io.WriteString(w, block)
	}))
	t.Cleanup(srv.Close)
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainTETH, NativeSymbol: "eth", RPCURL: srv.URL,
		ERC20Tokens: []types.Token{{Symbol: "usdc", Contract: usdc, Decimals: 6, ChainID: models.ChainTETH}}})

	transfers, err := adapter.ScanBlock(context.Background(), 100)

	if err != nil || len(transfers) != 2 {
		t.Fatalf("transfers %+v err %v", transfers, err)
	}
	for _, transfer := range transfers {
		wantNonce := uint64(7)
		if transfer.Token != nil {
			wantNonce = 8
			if !strings.EqualFold(transfer.From, router) {
				t.Fatalf("token transfer from %s, want the Transfer log's sender", transfer.From)
			}
		}
		if !strings.EqualFold(transfer.Sender, signer) || transfer.SenderNonce == nil || *transfer.SenderNonce != wantNonce {
			t.Fatalf("transfer %s: sender %s nonce %v, want %s and %d", transfer.TxHash, transfer.Sender, transfer.SenderNonce, signer, wantNonce)
		}
	}
}
