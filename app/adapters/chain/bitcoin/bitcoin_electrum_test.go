package bitcoin

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

type electrumHandler func(params []json.RawMessage) (any, *electrumError)

// fakeElectrum is an ElectrumX stand-in on a loopback listener.
type fakeElectrum struct {
	mu       sync.Mutex
	handlers map[string]electrumHandler
	methods  []string
}

func newFakeElectrum(t *testing.T, listener net.Listener) *fakeElectrum {
	t.Helper()
	fake := &fakeElectrum{handlers: map[string]electrumHandler{
		"server.version": func([]json.RawMessage) (any, *electrumError) { return []string{"ElectrumX 1.15.0", "1.4"}, nil },
		"server.features": func([]json.RawMessage) (any, *electrumError) {
			return map[string]any{"genesis_hash": ltcTestnetGenesisHash}, nil
		},
	}}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go fake.serve(conn)
		}
	}()
	return fake
}

func (f *fakeElectrum) handle(method string, h electrumHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func (f *fakeElectrum) called(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.methods {
		if m == method {
			n++
		}
	}
	return n
}

func (f *fakeElectrum) serve(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var req struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return
		}
		f.mu.Lock()
		f.methods = append(f.methods, req.Method)
		h := f.handlers[req.Method]
		f.mu.Unlock()
		// A notification before the answer must be skipped by the client.
		_, _ = fmt.Fprintf(conn, `{"jsonrpc":"2.0","method":"blockchain.headers.subscribe","params":[{"height":1}]}`+"\n")
		var resp map[string]any
		if h == nil {
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "unknown method " + req.Method}}
		} else if result, rpcErr := h(req.Params); rpcErr != nil {
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": rpcErr}
		} else {
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
		}
		line, _ := json.Marshal(resp)
		_, _ = conn.Write(append(line, '\n'))
	}
}

func newTCPFakeElectrum(t *testing.T) (*fakeElectrum, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return newFakeElectrum(t, listener), electrumTCPScheme + listener.Addr().String()
}

func ltcTestnetElectrum(t *testing.T, rawURL string) *electrumProvider {
	t.Helper()
	network := bitcoinNetworkOf(BitcoinConfig{ChainIDStr: models.ChainTLTC, IsTestnet: true})
	provider, err := newElectrumProvider("fallback-1", rawURL, network, models.NativeLTC)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestElectrumProvider_ScriptHashOfAP2WPKHAddress(t *testing.T) {
	provider := ltcTestnetElectrum(t, "electrum+tcp://127.0.0.1:1")
	// Asked of electrum-ltc.bysh.me (testnet), which listed this address's UTXO.
	got, err := provider.scriptHash("tltc1q4nhl6f48sz3hqjcknptjdh7hgkt253ul6kq4dx")
	if err != nil {
		t.Fatal(err)
	}
	if got != "884c81ee3d4c3114d895e57934556732ac4fd37127ebfbbbb709c1ced3cf0bd1" {
		t.Fatalf("script hash = %s", got)
	}
}

func TestElectrumProvider_ServesReadsAndBroadcast(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	provider := ltcTestnetElectrum(t, rawURL)
	raw, txid := failoverTestRawTx(t)
	header := make([]byte, blockHeaderBytes)
	header[0] = 0x20
	blockHash := reversedHex(hash256(header))

	fake.handle("blockchain.scripthash.listunspent", func([]json.RawMessage) (any, *electrumError) {
		return []map[string]any{
			{"tx_hash": failoverTestUTXOTx, "tx_pos": 1, "height": 4907446, "value": 49859},
			{"tx_hash": strings.Repeat("2", 64), "tx_pos": 0, "height": 0, "value": 5},
		}, nil
	})
	fake.handle("blockchain.scripthash.get_balance", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"confirmed": 49859, "unconfirmed": 5}, nil
	})
	fake.handle("blockchain.headers.subscribe", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"height": 100, "hex": ""}, nil
	})
	fake.handle("blockchain.transaction.get", func(params []json.RawMessage) (any, *electrumError) {
		if string(params[0]) == `"`+failoverTestUTXOTx+`"` {
			return map[string]any{"blockhash": blockHash, "confirmations": 3}, nil
		}
		return nil, &electrumError{Code: 2, Message: "daemon error: DaemonError({'code': -5, 'message': 'No such mempool or blockchain transaction. Use gettransaction for wallet transactions.'})"}
	})
	fake.handle("blockchain.block.header", func(params []json.RawMessage) (any, *electrumError) {
		if string(params[0]) != "98" {
			return nil, &electrumError{Code: 1, Message: "wrong height " + string(params[0])}
		}
		return hex.EncodeToString(header), nil
	})
	fake.handle("blockchain.estimatefee", func([]json.RawMessage) (any, *electrumError) { return 1.008e-05, nil })
	fake.handle("blockchain.transaction.broadcast", func(params []json.RawMessage) (any, *electrumError) {
		if string(params[0]) != `"`+hex.EncodeToString(raw)+`"` {
			return nil, &electrumError{Code: 1, Message: "unexpected bytes"}
		}
		return txid, nil
	})
	ctx := context.Background()

	utxos, err := provider.confirmedUTXOs(ctx, failoverTestAddress)
	if err != nil || len(utxos) != 1 || utxos[0].Value != 49859 || utxos[0].Vout != 1 {
		t.Fatalf("utxos = %+v, %v", utxos, err)
	}
	balance, err := provider.balance(ctx, failoverTestAddress)
	if err != nil || balance.Amount.Int64() != 49864 || balance.Asset != models.NativeLTC {
		t.Fatalf("balance = %+v, %v", balance, err)
	}
	if tip, err := provider.latestBlock(ctx); err != nil || tip != 100 {
		t.Fatalf("tip = %d, %v", tip, err)
	}
	if height, err := provider.transactionBlock(ctx, failoverTestUTXOTx); err != nil || height != 98 {
		t.Fatalf("tx block = %d, %v; want tip 100 - 3 confirmations + 1", height, err)
	}
	if height, err := provider.transactionBlock(ctx, strings.Repeat("3", 64)); err != nil || height != 0 {
		t.Fatalf("unknown tx = %d, %v; want pending", height, err)
	}
	if rate, err := provider.feeRate(ctx); err != nil || rate != 1008 {
		t.Fatalf("fee rate = %d, %v; want 1008 milli-sat/vB", rate, err)
	}
	if got, err := provider.broadcast(ctx, raw); err != nil || got != txid {
		t.Fatalf("broadcast = %q, %v", got, err)
	}
	if _, err := provider.scanBlock(ctx, 1); err != errProviderUnsupported {
		t.Fatalf("scan block err = %v", err)
	}
	if fake.called("server.features") != 1 {
		t.Fatalf("genesis checked %d times, want once", fake.called("server.features"))
	}
}

func TestElectrumProvider_RefusesAHeaderThatIsNotTheTransactionsBlock(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	provider := ltcTestnetElectrum(t, rawURL)
	fake.handle("blockchain.transaction.get", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"blockhash": strings.Repeat("ab", 32), "confirmations": 1}, nil
	})
	fake.handle("blockchain.headers.subscribe", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"height": 100}, nil
	})
	fake.handle("blockchain.block.header", func([]json.RawMessage) (any, *electrumError) {
		return hex.EncodeToString(make([]byte, blockHeaderBytes)), nil
	})
	_, err := provider.transactionBlock(context.Background(), failoverTestUTXOTx)
	if err == nil || !strings.Contains(err.Error(), "index and daemon disagree") {
		t.Fatalf("err = %v", err)
	}
}

func TestElectrumProvider_WrongGenesisAndRejection(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	provider := ltcTestnetElectrum(t, rawURL)
	fake.handle("server.features", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"genesis_hash": ltcMainnetGenesisHash}, nil
	})
	if _, err := provider.latestBlock(context.Background()); err == nil || !strings.Contains(err.Error(), "serves genesis") {
		t.Fatalf("err = %v, want the mainnet server refused", err)
	}

	fake.handle("server.features", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"genesis_hash": ltcTestnetGenesisHash}, nil
	})
	fake.handle("blockchain.transaction.broadcast", func([]json.RawMessage) (any, *electrumError) {
		return nil, &electrumError{Code: electrumRejectedCode, Message: "the transaction was rejected by network rules.\n\nbad-txns-inputs-missingorspent"}
	})
	raw, _ := failoverTestRawTx(t)
	_, err := provider.broadcast(context.Background(), raw)
	var rejected *btcBroadcastRejectedError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "missingorspent") {
		t.Fatalf("err = %v, want a rejection", err)
	}
}

// TestBitcoinFailover_ElectrumFallbackServesWhatItCan: with the Esplora primary down,
// UTXOs come from Electrum and a block scan goes to the next provider that can scan.
func TestBitcoinFailover_ElectrumFallbackServesWhatItCan(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	primary.down.Store(true)
	fake, electrumURL := newTCPFakeElectrum(t)
	fake.handle("blockchain.scripthash.listunspent", func([]json.RawMessage) (any, *electrumError) {
		return []map[string]any{{"tx_hash": failoverTestUTXOTx, "tx_pos": 1, "height": 4907446, "value": 49859}}, nil
	})
	live := newFailoverTestLive(t, primarySrv.URL, electrumURL)

	utxos, err := live.listConfirmedUTXOs(context.Background(), failoverTestAddress)
	if err != nil || len(utxos) != 1 {
		t.Fatalf("utxos = %+v, %v", utxos, err)
	}
	_, err = live.ScanBlock(context.Background(), 4907446)
	if err == nil || !strings.Contains(err.Error(), errProviderUnsupported.Error()) {
		t.Fatalf("scan err = %v, want electrum skipped as unsupported", err)
	}
	if live.providers.members[1].consecutiveFailures != 0 {
		t.Fatal("an unsupported operation must not count against the provider")
	}
}

func TestElectrumProvider_CertificatePin(t *testing.T) {
	cert, fingerprint := selfSignedCert(t, "electrum.test")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeElectrum(t, listener)
	fake.handle("blockchain.headers.subscribe", func([]json.RawMessage) (any, *electrumError) {
		return map[string]any{"height": 7}, nil
	})
	base := electrumSSLScheme + listener.Addr().String() + "?" + electrumCertPinParam + "="

	pinned := ltcTestnetElectrum(t, base+fingerprint)
	if tip, err := pinned.latestBlock(context.Background()); err != nil || tip != 7 {
		t.Fatalf("pinned tip = %d, %v", tip, err)
	}
	wrong := ltcTestnetElectrum(t, base+strings.Repeat("00", sha256.Size))
	if _, err := wrong.latestBlock(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match the pin") {
		t.Fatalf("wrong pin err = %v", err)
	}
	unpinned := ltcTestnetElectrum(t, electrumSSLScheme+listener.Addr().String())
	if _, err := unpinned.latestBlock(context.Background()); err == nil {
		t.Fatal("a self-signed certificate without a pin must be refused")
	}
}

func selfSignedCert(t *testing.T, host string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{host},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:])
}
