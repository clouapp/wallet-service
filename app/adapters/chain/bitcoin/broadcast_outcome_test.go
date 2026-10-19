package bitcoin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

func TestBroadcast_ARefusedConnectionWasNeverSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("closed server was dialed")
	}))
	endpoint := srv.URL
	srv.Close()

	t.Run("rpc", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		assertNeverSentBroadcast(t, live)
	})
	t.Run("rest", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		live.restAPI = true
		live.http = httpclient.NewClient(time.Second)
		assertNeverSentBroadcast(t, live)
	})
}

// droppingServer reads each request and drops the connection without an answer:
// the bytes reached it, the outcome is unknown.
func droppingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBroadcast_Unknown_OutcomeIsNotAKnownFailure(t *testing.T) {
	endpoint := droppingServer(t)
	t.Run("rpc", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		assertUnknownBroadcast(t, live)
	})
	t.Run("rest", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		live.restAPI = true
		live.http = httpclient.NewClient(time.Second)
		assertUnknownBroadcast(t, live)
	})
}

func TestBroadcast_Server_ErrorStaysAKnownFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	live := NewBitcoinLive(BitcoinConfig{RPCURL: srv.URL, NativeSymbol: "BTC"})
	live.restAPI = true
	live.http = httpclient.Wrap(srv.Client())
	_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if !chain.KnownFailure(err) || errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("HTTP 502 broadcast: %v", err)
	}
}

func TestBroadcast_ABitcoindRejectionCarriedByHTTP500IsARejection(t *testing.T) {
	raw, txid := failoverTestRawTx(t)
	answering := func(body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// bitcoind before v28 and litecoind answer every JSON-RPC error with HTTP 500.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}

	conflict := NewBitcoinLive(BitcoinConfig{RPCURL: answering(`{"result":null,"error":{"code":-26,"message":"txn-mempool-conflict"},"id":1}`), NativeSymbol: "BTC"})
	_, err := conflict.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw, TxHash: txid})
	var rejected *btcBroadcastRejectedError
	if !errors.As(err, &rejected) || errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("a -26 refusal carried by HTTP 500 is the node's rejection, not an unknown outcome: %v", err)
	}

	known := NewBitcoinLive(BitcoinConfig{RPCURL: answering(`{"result":null,"error":{"code":-27,"message":"Transaction already in block chain"},"id":1}`), NativeSymbol: "BTC"})
	if got, err := known.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw, TxHash: txid}); err != nil || got != txid {
		t.Fatalf("already in the chain = %q, %v; want the signed txid %s", got, err, txid)
	}

	generic := NewBitcoinLive(BitcoinConfig{RPCURL: answering(`<html>500 Internal Server Error</html>`), NativeSymbol: "BTC"})
	if _, err := generic.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: raw, TxHash: txid}); !errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("an HTTP 500 without a JSON-RPC answer may have relayed the bytes: %v", err)
	}
}

func assertNeverSentBroadcast(t *testing.T, live *BitcoinLive) {
	t.Helper()
	_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if err == nil || errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("a broadcast whose connection was refused reached no node: %v", err)
	}
}

func assertUnknownBroadcast(t *testing.T, live *BitcoinLive) {
	t.Helper()
	_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if !errors.Is(err, chain.ErrUnknownOutcome) || chain.KnownFailure(err) {
		t.Fatalf("broadcast err %v", err)
	}
}

func TestElectrumBroadcast_TimeoutAfterSendIsAnUnknownOutcome(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fake.handle("blockchain.transaction.broadcast", func([]json.RawMessage) (any, *electrumError) {
		<-release
		return nil, nil
	})
	provider := ltcTestnetElectrum(t, rawURL)
	raw, _ := failoverTestRawTx(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := provider.broadcast(ctx, raw)
	if !errors.Is(err, chain.ErrUnknownOutcome) || chain.KnownFailure(err) {
		t.Fatalf("broadcast that timed out after send: %v", err)
	}
}

func TestElectrumBroadcast_DroppedConnectionAfterSendIsAnUnknownOutcome(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go dropOnBroadcast(conn)
		}
	}()
	provider := ltcTestnetElectrum(t, electrumTCPScheme+listener.Addr().String())
	raw, _ := failoverTestRawTx(t)

	_, err = provider.broadcast(context.Background(), raw)
	if !errors.Is(err, chain.ErrUnknownOutcome) || chain.KnownFailure(err) {
		t.Fatalf("broadcast whose connection dropped after send: %v", err)
	}
}

func TestElectrumBroadcast_UnreachableServerIsNotAnUnknownOutcome(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	provider := ltcTestnetElectrum(t, electrumTCPScheme+address)
	raw, _ := failoverTestRawTx(t)

	_, err = provider.broadcast(context.Background(), raw)
	if err == nil || errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("broadcast that never reached a server: %v", err)
	}
}

// dropOnBroadcast answers the handshake, then closes the connection as soon
// as the broadcast request has been read.
func dropOnBroadcast(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return
		}
		var result any
		switch req.Method {
		case "server.version":
			result = []string{"ElectrumX 1.15.0", "1.4"}
		case "server.features":
			result = map[string]any{"genesis_hash": ltcTestnetGenesisHash}
		default:
			return
		}
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		_, _ = conn.Write(append(line, '\n'))
	}
}
