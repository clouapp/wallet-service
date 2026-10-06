package xrp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const xrpTestClassic = "rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTh"

func TestLiveReadsDropsAndRefusesPayments(t *testing.T) {
	t.Parallel()

	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		switch {
		case strings.Contains(text, "account_info"):
			calls = append(calls, "account_info")
			_, _ = io.WriteString(w, `{"result":{"status":"success","account_data":{"Balance":"1000000"}}}`)
		case strings.Contains(text, `"method":"ledger"`):
			calls = append(calls, "ledger")
			_, _ = io.WriteString(w, `{"result":{"status":"success","ledger":{"ledger_index":42}}}`)
		default:
			t.Errorf("unexpected rpc %s", text)
			http.Error(w, "no", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	adapter := NewLive(Config{
		ChainIDStr: models.ChainTXRP, ChainName: "XRP Ledger Testnet", NativeSymbol: models.NativeXRP,
		RPCURL: server.URL, IsTestnet: true, Confirmations: 1,
	})
	if !adapter.IsTestnet() || adapter.ID() != models.ChainTXRP || adapter.NativeAsset() != models.NativeXRP {
		t.Fatalf("config %+v", adapter.cfg)
	}
	if !adapter.ValidateAddress(xrpTestClassic) || adapter.ValidateAddress("0xabc") {
		t.Fatal("classic address check")
	}

	balance, err := adapter.GetBalance(context.Background(), xrpTestClassic)
	if err != nil || balance.Amount.String() != "1000000" || balance.Human != "1" || balance.Decimals != NativeDecimals {
		t.Fatalf("balance %+v err %v", balance, err)
	}
	index, err := adapter.GetLatestBlock(context.Background())
	if err != nil || index != 42 {
		t.Fatalf("ledger %d err %v", index, err)
	}
	if _, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{}); err != ErrPaymentsNotImplemented {
		t.Fatalf("build: %v", err)
	}
	if _, err := adapter.SignTransaction(context.Background(), &types.UnsignedTx{}, []byte{1}); err != ErrPaymentsNotImplemented {
		t.Fatalf("sign: %v", err)
	}
	if _, err := adapter.BroadcastTransaction(context.Background(), &types.SignedTx{}); err != ErrPaymentsNotImplemented {
		t.Fatalf("broadcast: %v", err)
	}
	if _, err := adapter.BuildSweep(context.Background(), types.SweepRequest{}); err != ErrPaymentsNotImplemented {
		t.Fatalf("sweep: %v", err)
	}
	if strings.Join(calls, ",") != "account_info,ledger" {
		t.Fatalf("rpc calls %v, payments must not reach the server", calls)
	}
}

func TestLiveMissingAccountIsZeroAndFloatBalanceIsRefused(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), xrpTestClassic) {
			_, _ = io.WriteString(w, `{"result":{"status":"error","error":"actNotFound"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":{"status":"success","account_data":{"Balance":1.5}}}`)
	}))
	t.Cleanup(server.Close)
	adapter := NewLive(Config{ChainIDStr: models.ChainXRP, NativeSymbol: models.NativeXRP, RPCURL: server.URL, IsTestnet: true})

	missing, err := adapter.GetBalance(context.Background(), xrpTestClassic)
	if err != nil || missing.Amount.Sign() != 0 || missing.Human != "0" {
		t.Fatalf("missing %+v err %v", missing, err)
	}
	adapter.ReplaceEndpoint(server.URL)
	if _, err := adapter.GetBalance(context.Background(), "rJrRMgiRgrU6hDF4pgu5DXQdWyPbY35ErN"); err == nil {
		t.Fatal("a JSON number balance was accepted")
	}
}

func TestParseDropsRejectsNonIntegers(t *testing.T) {
	t.Parallel()

	drops, err := parseDrops([]byte(`"2500000"`))
	if err != nil || drops.String() != "2500000" {
		t.Fatalf("drops %v err %v", drops, err)
	}
	for _, raw := range []string{`1`, `1.5`, `""`, `"01.0"`, `"-1"`, `"1e6"`} {
		if _, err := parseDrops([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
