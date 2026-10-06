package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	failoverTestAddress = "tltc1qc3xpdkwnfzcvkrd8kkpn2v5rx9x2ectxmrw8we"
	failoverTestUTXOTx  = "8ab2ea8689c293e4cb441752263765fc27b56fd414d55c6d7f994866a7dba19d"
)

// failoverTestRawTx is a decodable one-input, one-output transaction and its txid.
func failoverTestRawTx(t *testing.T) ([]byte, string) {
	t.Helper()
	prev, err := chainhash.NewHashFromStr(failoverTestUTXOTx)
	if err != nil {
		t.Fatal(err)
	}
	msg := wire.NewMsgTx(wire.TxVersion)
	msg.AddTxIn(wire.NewTxIn(wire.NewOutPoint(prev, 1), nil, nil))
	msg.AddTxOut(wire.NewTxOut(40_000, []byte{0x00, 0x14, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}))
	var buf bytes.Buffer
	if err := msg.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), msg.TxHash().String()
}

// failoverEsplora answers like Litecoin Space testnet; down makes every call a 522.
type failoverEsplora struct {
	t         *testing.T
	down      atomic.Bool
	genesis   string
	calls     atomic.Int64
	broadcast func(body string) (int, string)
	posted    []string
}

func newFailoverEsplora(t *testing.T) (*failoverEsplora, *httptest.Server) {
	fake := &failoverEsplora{t: t, genesis: ltcTestnetGenesisHash}
	srv := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(srv.Close)
	return fake, srv
}

const cloudflareOriginTimeout = 522

func (f *failoverEsplora) serve(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if f.down.Load() {
		w.WriteHeader(cloudflareOriginTimeout)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/tx":
		body, _ := io.ReadAll(r.Body)
		f.posted = append(f.posted, string(body))
		status, answer := f.broadcast(string(body))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	case r.URL.Path == "/block-height/0":
		_, _ = io.WriteString(w, f.genesis)
	case r.URL.Path == "/blocks/tip/height":
		_, _ = io.WriteString(w, "4907461")
	case r.URL.Path == "/address/"+failoverTestAddress+"/utxo":
		_, _ = io.WriteString(w, `[{"txid":"`+failoverTestUTXOTx+`","vout":1,"value":49859,"status":{"confirmed":true}},`+
			`{"txid":"`+strings.Repeat("1", 64)+`","vout":0,"value":7,"status":{"confirmed":false}}]`)
	case r.URL.Path == "/tx/"+failoverTestUTXOTx+"/status":
		_, _ = io.WriteString(w, `{"confirmed":true,"block_height":4907446}`)
	case r.URL.Path == "/fee-estimates":
		_, _ = io.WriteString(w, `{"3":2.5,"6":1.2}`)
	default:
		http.NotFound(w, r)
	}
}

func newFailoverTestLive(t *testing.T, primaryURL string, fallbacks ...string) *BitcoinLive {
	t.Helper()
	cfg := BitcoinConfig{ChainIDStr: models.ChainTLTC, NativeSymbol: models.NativeLTC, RPCURL: primaryURL, IsTestnet: true}
	for _, url := range fallbacks {
		cfg.Fallbacks = append(cfg.Fallbacks, BitcoinFallback{URL: url})
	}
	live := NewBitcoinLive(cfg)
	for _, member := range live.providers.members {
		if direct, ok := member.provider.(directProvider); ok {
			direct.live.restAPI = true
			direct.live.esploraRetry.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
		}
	}
	return live
}

func TestBitcoinFailover_ReadsMoveToTheFallbackWhenThePrimaryIsDown(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	fallback, fallbackSrv := newFailoverEsplora(t)
	primary.down.Store(true)
	live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)
	ctx := context.Background()

	utxos, err := live.listConfirmedUTXOs(ctx, failoverTestAddress)
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	if len(utxos) != 1 || utxos[0].TxID != failoverTestUTXOTx || utxos[0].Value != 49859 {
		t.Fatalf("utxos = %+v, want the one confirmed output", utxos)
	}
	balance, err := live.GetBalance(ctx, failoverTestAddress)
	if err != nil || balance.Amount.Int64() != 49866 {
		t.Fatalf("balance = %+v, %v; want 49866 (confirmed + unconfirmed)", balance, err)
	}
	tip, err := live.GetLatestBlock(ctx)
	if err != nil || tip != 4907461 {
		t.Fatalf("tip = %d, %v", tip, err)
	}
	height, err := live.GetTransactionBlock(ctx, failoverTestUTXOTx)
	if err != nil || height != 4907446 {
		t.Fatalf("tx block = %d, %v", height, err)
	}
	rate, err := live.fetchFeeRate(ctx)
	if err != nil || rate != 2500 {
		t.Fatalf("fee rate = %d milli-sat/vB, %v; want 2500", rate, err)
	}
	if fallback.calls.Load() == 0 {
		t.Fatal("fallback was never called")
	}
}

func TestBitcoinFailover_BothDownIsOneClearError(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	fallback, fallbackSrv := newFailoverEsplora(t)
	primary.down.Store(true)
	fallback.down.Store(true)
	live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

	_, err := live.listConfirmedUTXOs(context.Background(), failoverTestAddress)
	if err == nil {
		t.Fatal("want an error when every provider is down")
	}
	for _, want := range []string{"every provider failed", "primary (esplora)", "fallback-1 (esplora)", "522"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), primarySrv.URL) || strings.Contains(err.Error(), fallbackSrv.URL) {
		t.Errorf("error %q leaks a provider URL", err)
	}
}

func TestBitcoinFailover_FallbackOnTheWrongNetworkIsNeverUsed(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	fallback, fallbackSrv := newFailoverEsplora(t)
	primary.down.Store(true)
	fallback.genesis = ltcMainnetGenesisHash
	live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

	_, err := live.listConfirmedUTXOs(context.Background(), failoverTestAddress)
	if err == nil || !strings.Contains(err.Error(), "serves genesis "+ltcMainnetGenesisHash) {
		t.Fatalf("err = %v, want the mainnet fallback refused", err)
	}
}

func TestBitcoinFailover_BroadcastResendsTheSameBytesOnlyWhenUndecided(t *testing.T) {
	raw, txid := failoverTestRawTx(t)
	rawHex := hex.EncodeToString(raw)

	t.Run("primary unavailable: same bytes to the fallback", func(t *testing.T) {
		primary, primarySrv := newFailoverEsplora(t)
		fallback, fallbackSrv := newFailoverEsplora(t)
		primary.broadcast = func(string) (int, string) { return http.StatusBadGateway, "upstream down" }
		fallback.broadcast = func(string) (int, string) { return http.StatusOK, txid }
		live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

		got, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
		if err != nil || got != txid {
			t.Fatalf("broadcast = %q, %v; want %s", got, err, txid)
		}
		if len(primary.posted) != 1 || len(fallback.posted) != 1 || primary.posted[0] != rawHex || fallback.posted[0] != rawHex {
			t.Fatalf("posted primary=%v fallback=%v, want the same raw hex once each", primary.posted, fallback.posted)
		}
	})

	t.Run("primary rejects: the fallback is not asked", func(t *testing.T) {
		primary, primarySrv := newFailoverEsplora(t)
		fallback, fallbackSrv := newFailoverEsplora(t)
		primary.broadcast = func(string) (int, string) {
			return http.StatusBadRequest, `sendrawtransaction RPC error: {"code":-26,"message":"min relay fee not met"}`
		}
		fallback.broadcast = func(string) (int, string) { return http.StatusOK, txid }
		live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

		_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
		var rejected *btcBroadcastRejectedError
		if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "min relay fee not met") {
			t.Fatalf("err = %v, want the primary's rejection", err)
		}
		if len(fallback.posted) != 0 {
			t.Fatalf("fallback got %d broadcasts after a rejection", len(fallback.posted))
		}
	})

	t.Run("already known after a lost answer is success", func(t *testing.T) {
		primary, primarySrv := newFailoverEsplora(t)
		fallback, fallbackSrv := newFailoverEsplora(t)
		primary.down.Store(true)
		fallback.broadcast = func(string) (int, string) {
			return http.StatusBadRequest, `sendrawtransaction RPC error: {"code":-26,"message":"txn-already-in-mempool"}`
		}
		live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

		got, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
		if err != nil || got != txid {
			t.Fatalf("broadcast = %q, %v; want the local txid %s", got, err, txid)
		}
	})

	t.Run("a provider answering another txid is an error", func(t *testing.T) {
		fake, srv := newFailoverEsplora(t)
		fake.broadcast = func(string) (int, string) { return http.StatusOK, strings.Repeat("ab", 32) }
		live := newFailoverTestLive(t, srv.URL)

		_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
		if err == nil || !strings.Contains(err.Error(), "the signed transaction is "+txid) {
			t.Fatalf("err = %v, want a txid mismatch", err)
		}
	})

	t.Run("both unavailable", func(t *testing.T) {
		primary, primarySrv := newFailoverEsplora(t)
		fallback, fallbackSrv := newFailoverEsplora(t)
		primary.down.Store(true)
		fallback.down.Store(true)
		live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)

		_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw})
		if err == nil || !strings.Contains(err.Error(), "no provider accepted the transaction") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBitcoinFailover_BreakerSkipsAFailingPrimaryUntilItsCooldownEnds(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	_, fallbackSrv := newFailoverEsplora(t)
	primary.down.Store(true)
	live := newFailoverTestLive(t, primarySrv.URL, fallbackSrv.URL)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	live.providers.now = func() time.Time { return now }
	ctx := context.Background()

	for range btcProviderFailuresToOpen {
		if _, err := live.GetLatestBlock(ctx); err != nil {
			t.Fatal(err)
		}
	}
	callsWhenOpened := primary.calls.Load()
	if _, err := live.GetLatestBlock(ctx); err != nil {
		t.Fatal(err)
	}
	if primary.calls.Load() != callsWhenOpened {
		t.Fatalf("primary called while its breaker is open")
	}

	primary.down.Store(false)
	now = now.Add(btcProviderCooldownBase)
	if _, err := live.GetLatestBlock(ctx); err != nil {
		t.Fatal(err)
	}
	if primary.calls.Load() == callsWhenOpened {
		t.Fatalf("primary not retried after the cooldown")
	}
	if !live.providers.members[0].available(now) {
		t.Fatalf("a success must close the breaker")
	}
}

func TestBitcoinFailover_CooldownDoublesUpToTheMaximum(t *testing.T) {
	member := &btcProviderMember{}
	now := time.Unix(0, 0)
	var cooldowns []time.Duration
	for range 6 {
		for range btcProviderFailuresToOpen - 1 {
			if member.failed(now) != 0 {
				t.Fatal("breaker opened before the threshold")
			}
		}
		cooldowns = append(cooldowns, member.failed(now))
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, btcProviderCooldownMax, btcProviderCooldownMax}
	for i := range want {
		if cooldowns[i] != want[i] {
			t.Fatalf("cooldowns = %v, want %v", cooldowns, want)
		}
	}
}

func TestBitcoinFallbackProvider_RejectsUnsupportedURLs(t *testing.T) {
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainTLTC, RPCURL: "https://litecoinspace.org/testnet/api",
		Fallbacks: []BitcoinFallback{
			{URL: "ftp://example.com"},
			{URL: "electrum+tcp://electrum.example.com:51001"},
			{URL: "electrum+ssl://electrum.example.com:51002?cert_sha256=nothex"},
			{URL: "electrum+ssl://electrum.example.com:51002?cert_sha256=" + strings.Repeat("ab", 32)},
			{URL: "https://litecoin-testnet.gateway.tatum.io", APIKey: "k"},
		},
	})
	if got := len(live.providers.members); got != 3 {
		t.Fatalf("members = %d, want primary + pinned electrum + tatum", got)
	}
	if label := live.providers.members[1].provider.label(); label != "fallback-4 (electrum)" {
		t.Fatalf("label = %q", label)
	}
	tatum := live.providers.members[2].provider.(tatumProvider)
	if tatum.label() != "fallback-5 (tatum)" || tatum.gateway.live.rpc.headers[apiKeyHeader] != "k" {
		t.Fatalf("tatum fallback = %q headers=%v", tatum.label(), tatum.gateway.live.rpc.headers)
	}
}
