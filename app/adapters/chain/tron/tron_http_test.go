package tron

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const tronTestAPIKey = "test-key-not-a-secret"

func newTronHTTPAdapter(t *testing.T, handler http.HandlerFunc, apiKey string) *TronLive {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adapter := NewTronLive(TronConfig{ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, RPCURL: server.URL + "/", APIKey: apiKey})
	adapter.retry.sleep = func(context.Context, time.Duration) error { return nil }
	return adapter
}

func TestTronHTTPSendsAPIKeyOnlyWhenConfiguredAndNeverInErrors(t *testing.T) {
	var seen atomic.Value
	handler := func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get(tronAPIKeyHeader))
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	adapter := newTronHTTPAdapter(t, handler, tronTestAPIKey)
	_, err := adapter.GetLatestBlock(context.Background())
	if err == nil {
		t.Fatal("HTTP 500 accepted")
	}
	if seen.Load() != tronTestAPIKey {
		t.Fatalf("API key header = %q", seen.Load())
	}
	if strings.Contains(err.Error(), tronTestAPIKey) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks the key or URL: %v", err)
	}

	withoutKey := newTronHTTPAdapter(t, handler, "")
	_, _ = withoutKey.GetLatestBlock(context.Background())
	if seen.Load() != "" {
		t.Fatalf("API key header sent without a key: %q", seen.Load())
	}
}

func TestTronHTTPRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"blockID":"`+tronTestHeadBlockID+`","block_header":{"raw_data":{"number":71532773,"timestamp":1791126834000}}}`)
	}, "")
	head, err := adapter.GetLatestBlock(context.Background())
	if err != nil || head != uint64(tronTestHeadNumber) || calls.Load() != 3 {
		t.Fatalf("head %d after %d calls: %v", head, calls.Load(), err)
	}

	calls.Store(0)
	limited := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}, "")
	if _, err := limited.GetLatestBlock(context.Background()); !errors.Is(err, chain.ErrRateLimited) || calls.Load() != tronRateLimitMaxAttempts {
		t.Fatalf("persistent 429 after %d calls: %v", calls.Load(), err)
	}
}

func TestTronHTTPNodeErrorMemberAndHeadFallback(t *testing.T) {
	adapter := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Error":"class java.lang.IllegalArgumentException : invalid address"}`)
	}, "")
	if _, err := adapter.GetBalance(context.Background(), "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Fatalf("Error member: %v", err)
	}

	// Nodes without /wallet/getblock answer 404; the head comes from getnowblock.
	fallback := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wallet/getblock" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"blockID":"`+tronTestHeadBlockID+`","block_header":{"raw_data":{"number":71532773,"timestamp":1791126834000}},"transactions":[]}`)
	}, "")
	if head, err := fallback.GetLatestBlock(context.Background()); err != nil || head != uint64(tronTestHeadNumber) {
		t.Fatalf("getnowblock fallback: %d, %v", head, err)
	}
}

func TestTronBalancesAndAccountReads(t *testing.T) {
	node := newFakeTronNode(t)
	adapter := newTronTestAdapter(t, node)
	_, funded := tronTestKey(t, tronTestSenderKeyHex)
	_, unknown := tronTestKey(t, tronTestOtherKeyHex)
	node.fund(t, funded, 12_345_678)
	node.holdTokens(t, funded, 9_000_001)

	balance, err := adapter.GetBalance(context.Background(), funded)
	if err != nil || balance.Amount.Int64() != 12_345_678 || balance.Human != "12.345678" || balance.Decimals != 6 {
		t.Fatalf("funded balance %+v, %v", balance, err)
	}
	balance, err = adapter.GetBalance(context.Background(), unknown)
	if err != nil || balance.Amount.Sign() != 0 {
		t.Fatalf("never-activated balance %+v, %v", balance, err)
	}
	token, err := adapter.GetTokenBalance(context.Background(), funded, tronTestNileUSDT)
	if err != nil || token.Amount.Int64() != 9_000_001 || token.Asset != models.SymbolUSDT {
		t.Fatalf("token balance %+v, %v", token, err)
	}
	if _, err := adapter.GetBalance(context.Background(), "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"); err == nil {
		t.Fatal("EVM address accepted")
	}
	if !adapter.ValidateAddress(funded) || adapter.ValidateAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf") {
		t.Fatal("ValidateAddress")
	}
	if !adapter.IsTestnet() || adapter.RequiredConfirmations() != 20 || adapter.NativeAsset() != models.NativeTRX {
		t.Fatal("config accessors")
	}
	if _, err := adapter.DeriveAddress(make([]byte, 32), 0); err == nil {
		t.Fatal("DeriveAddress must refuse")
	}
}

// The broadcast is exercised against a local server only.
func TestTronBroadcastTransactionLocalServer(t *testing.T) {
	adapter, unsigned, key, _ := buildTestTRXTransfer(t)
	signed, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(key))
	if err != nil {
		t.Fatal(err)
	}

	var posted atomic.Value
	answer := `{"result":true,"txid":"` + signed.TxHash + `"}`
	local := newTronHTTPAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wallet/broadcasthex" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		posted.Store(body["transaction"])
		_, _ = io.WriteString(w, answer)
	}, "")
	txID, err := local.BroadcastTransaction(context.Background(), signed)
	if err != nil || txID != signed.TxHash || posted.Load() != hex.EncodeToString(signed.RawBytes) {
		t.Fatalf("broadcast %s: %v", txID, err)
	}

	answer = `{"result":false,"code":"SIGERROR","message":"` + hex.EncodeToString([]byte("validate signature error")) + `"}`
	if _, err := local.BroadcastTransaction(context.Background(), signed); err == nil || !strings.Contains(err.Error(), "validate signature error") {
		t.Fatalf("refused broadcast: %v", err)
	}
	answer = `{"result":true,"txid":"` + strings.Repeat("00", 32) + `"}`
	if _, err := local.BroadcastTransaction(context.Background(), signed); err == nil {
		t.Fatal("mismatched txid accepted")
	}
	if _, err := local.BroadcastTransaction(context.Background(), &types.SignedTx{TxHash: strings.Repeat("ab", 32), RawBytes: signed.RawBytes}); err == nil {
		t.Fatal("tx hash not matching the raw data accepted")
	}
}
