package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const (
	esploraTestTxID      = "82ee8a1ca0969527da98c65bd7bd7aeeee897f19e0ae882c9addd4387a9436f7"
	esploraTestBlockHash = "00000000000000005cff77ae431593daf5c913c5a7109a08807da64503d5e2f0"
	esploraTestHeight    = uint64(154740)
)

type esploraAnswer struct {
	status int
	body   string
}

// fakeEsplora answers GETs by path; each path may queue answers, the last repeating.
type fakeEsplora struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string][]esploraAnswer
	hits   map[string]int
}

func newFakeEsplora(t *testing.T) *fakeEsplora {
	t.Helper()
	f := &fakeEsplora{t: t, routes: map[string][]esploraAnswer{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("esplora must only receive GET, got %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.mu.Lock()
		f.hits[r.URL.Path]++
		answers, ok := f.routes[r.URL.Path]
		var answer esploraAnswer
		if ok {
			answer = answers[0]
			if len(answers) > 1 {
				f.routes[r.URL.Path] = answers[1:]
			}
		}
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEsplora) on(path string, answers ...esploraAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = answers
}

func (f *fakeEsplora) ok(path, body string) { f.on(path, esploraAnswer{http.StatusOK, body}) }

func (f *fakeEsplora) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeEsplora) totalHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.hits {
		total += n
	}
	return total
}

// adapter is a REST BitcoinLive against the fake whose rate-limit backoff records
// instead of sleeping.
func (f *fakeEsplora) adapter(sleeps *[]time.Duration) *BitcoinLive {
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainTBTC, NativeSymbol: models.NativeBTC, RPCURL: f.srv.URL + "/testnet4/api", IsTestnet: true})
	live.restAPI = true
	live.esploraRetry.jitter = func(time.Duration) time.Duration { return 0 }
	live.esploraRetry.sleep = func(ctx context.Context, d time.Duration) error {
		if sleeps != nil {
			*sleeps = append(*sleeps, d)
		}
		return ctx.Err()
	}
	return live
}

const esploraTestPrefix = "/testnet4/api"

func txStatusPath(txID string) string { return esploraTestPrefix + "/tx/" + txID + "/status" }

func TestGetTransactionBlockREST_ConfirmedReturnsTheBlockHeight(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(txStatusPath(esploraTestTxID), `{"confirmed":true,"block_height":154740,"block_hash":"`+esploraTestBlockHash+`","block_time":1790907938}`)

	height, err := esplora.adapter(nil).GetTransactionBlock(context.Background(), esploraTestTxID)

	if err != nil || height != esploraTestHeight {
		t.Fatalf("height %d err %v", height, err)
	}
}

func TestGetTransactionBlockREST_PendingCases(t *testing.T) {
	cases := map[string]esploraAnswer{
		"unconfirmed (mempool)":        {http.StatusOK, `{"confirmed":false}`},
		"unknown to mempool.space":     {http.StatusOK, `{"confirmed":false}`},
		"unknown to blockstream (404)": {http.StatusNotFound, "Transaction not found"},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			esplora := newFakeEsplora(t)
			esplora.on(txStatusPath(esploraTestTxID), answer)

			height, err := esplora.adapter(nil).GetTransactionBlock(context.Background(), esploraTestTxID)

			if err != nil || height != 0 {
				t.Fatalf("height %d err %v, want pending (0, nil)", height, err)
			}
		})
	}
}

func TestGetTransactionBlockREST_FailuresAreErrors(t *testing.T) {
	cases := map[string]esploraAnswer{
		"http 500":                   {http.StatusInternalServerError, "boom"},
		"http 400":                   {http.StatusBadRequest, "Invalid hex hash"},
		"not json":                   {http.StatusOK, "<html>"},
		"confirmed without a height": {http.StatusOK, `{"confirmed":true}`},
		"confirmed at height zero":   {http.StatusOK, `{"confirmed":true,"block_height":0}`},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			esplora := newFakeEsplora(t)
			esplora.on(txStatusPath(esploraTestTxID), answer)

			height, err := esplora.adapter(nil).GetTransactionBlock(context.Background(), esploraTestTxID)

			if err == nil {
				t.Fatalf("expected an error, got height %d", height)
			}
		})
	}
}

func TestGetTransactionBlockREST_RetriesRateLimitsThenAnswers(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.on(txStatusPath(esploraTestTxID),
		esploraAnswer{http.StatusTooManyRequests, "slow down"},
		esploraAnswer{http.StatusForbidden, "slow down"},
		esploraAnswer{http.StatusOK, `{"confirmed":true,"block_height":154740}`},
	)
	var sleeps []time.Duration

	height, err := esplora.adapter(&sleeps).GetTransactionBlock(context.Background(), esploraTestTxID)

	if err != nil || height != esploraTestHeight {
		t.Fatalf("height %d err %v", height, err)
	}
	if want := []time.Duration{esploraRateLimitBaseDelay, 2 * esploraRateLimitBaseDelay}; fmt.Sprint(sleeps) != fmt.Sprint(want) {
		t.Fatalf("backoff %v, want %v", sleeps, want)
	}
}

func TestGetTransactionBlockREST_GivesUpOnPersistentRateLimit(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.on(txStatusPath(esploraTestTxID), esploraAnswer{http.StatusTooManyRequests, "slow down"})
	var sleeps []time.Duration

	_, err := esplora.adapter(&sleeps).GetTransactionBlock(context.Background(), esploraTestTxID)

	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err %v", err)
	}
	if got := esplora.hitCount(txStatusPath(esploraTestTxID)); got != esploraRateLimitMaxAttempts {
		t.Fatalf("attempts %d, want %d", got, esploraRateLimitMaxAttempts)
	}
	var total time.Duration
	for _, d := range sleeps {
		total += d
	}
	if total > 4*time.Second {
		t.Fatalf("backoff total %s is too long for one call", total)
	}
}

func TestGetTransactionBlockREST_NormalizesAndValidatesTheTxID(t *testing.T) {
	esplora := newFakeEsplora(t)
	esplora.ok(txStatusPath(esploraTestTxID), `{"confirmed":true,"block_height":154740}`)
	live := esplora.adapter(nil)

	if height, err := live.GetTransactionBlock(context.Background(), " "+strings.ToUpper(esploraTestTxID)+" "); err != nil || height != esploraTestHeight {
		t.Fatalf("height %d err %v", height, err)
	}
	for _, bad := range []string{"", "abc", esploraTestTxID + "00", strings.Repeat("z", 64), "../../address/x/utxo"} {
		if _, err := live.GetTransactionBlock(context.Background(), bad); err == nil {
			t.Fatalf("txid %q accepted", bad)
		}
	}
	if esplora.totalHits() != 1 {
		t.Fatalf("invalid txids must not reach the API, hits %d", esplora.totalHits())
	}
}

// fakeBitcoind answers JSON-RPC methods with canned results or errors.
func fakeBitcoind(t *testing.T, answers map[string]string) *BitcoinLive {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rpc: %v", err)
			return
		}
		answer, ok := answers[req.Method]
		if !ok {
			t.Errorf("unexpected rpc %s", req.Method)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`))
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainTBTC, RPCURL: srv.URL, IsTestnet: true})
}

func TestGetTransactionBlockRPC(t *testing.T) {
	rawTxInBlock := `{"jsonrpc":"2.0","id":1,"result":{"txid":"` + esploraTestTxID + `","blockhash":"` + esploraTestBlockHash + `","confirmations":3}}`
	cases := []struct {
		name    string
		answers map[string]string
		want    uint64
		wantErr bool
	}{
		{"confirmed", map[string]string{
			"getrawtransaction": rawTxInBlock,
			"getblockheader":    `{"jsonrpc":"2.0","id":1,"result":{"height":154740,"confirmations":3}}`,
		}, esploraTestHeight, false},
		{"in mempool (no blockhash)", map[string]string{
			"getrawtransaction": `{"jsonrpc":"2.0","id":1,"result":{"txid":"` + esploraTestTxID + `"}}`,
		}, 0, false},
		{"unknown to the node (-5)", map[string]string{
			"getrawtransaction": `{"jsonrpc":"2.0","id":1,"error":{"code":-5,"message":"No such mempool or blockchain transaction"}}`,
		}, 0, false},
		{"block left the main chain", map[string]string{
			"getrawtransaction": rawTxInBlock,
			"getblockheader":    `{"jsonrpc":"2.0","id":1,"result":{"height":154740,"confirmations":-1}}`,
		}, 0, false},
		{"other rpc error", map[string]string{
			"getrawtransaction": `{"jsonrpc":"2.0","id":1,"error":{"code":-28,"message":"Loading block index"}}`,
		}, 0, true},
		{"header without height", map[string]string{
			"getrawtransaction": rawTxInBlock,
			"getblockheader":    `{"jsonrpc":"2.0","id":1,"result":{"confirmations":3}}`,
		}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := fakeBitcoind(t, tc.answers)

			height, err := live.GetTransactionBlock(context.Background(), esploraTestTxID)

			if (err != nil) != tc.wantErr || height != tc.want {
				t.Fatalf("height %d err %v, want %d (error %t)", height, err, tc.want, tc.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Block scanning
// ---------------------------------------------------------------------------

func scanTxID(i int) string { return fmt.Sprintf("%064x", i+1) }

func scanTxsPage(start, count int) string {
	txs := make([]map[string]interface{}, 0, count)
	for i := start; i < start+count; i++ {
		txs = append(txs, map[string]interface{}{
			"txid": scanTxID(i),
			"vout": []map[string]interface{}{
				{"scriptpubkey_address": fmt.Sprintf("tb1qaddr%d", i), "value": 1000 + i, "scriptpubkey_type": "v0_p2wpkh"},
				{"scriptpubkey_type": "op_return", "value": 0},
			},
			"status": map[string]interface{}{"confirmed": true, "block_height": esploraTestHeight},
		})
	}
	body, _ := json.Marshal(txs)
	return string(body)
}

func blockPath(suffix string) string {
	return esploraTestPrefix + "/block/" + esploraTestBlockHash + suffix
}

// esploraBlockOf serves block esploraTestHeight with txCount transactions.
func esploraBlockOf(t *testing.T, txCount int) *fakeEsplora {
	esplora := newFakeEsplora(t)
	esplora.ok(fmt.Sprintf("%s/block-height/%d", esploraTestPrefix, esploraTestHeight), esploraTestBlockHash)
	esplora.ok(blockPath(""), fmt.Sprintf(`{"id":"%s","height":%d,"timestamp":1790907938,"tx_count":%d}`, esploraTestBlockHash, esploraTestHeight, txCount))
	for start := 0; start < txCount; start += esploraBlockTxsPageSize {
		esplora.ok(blockPath(fmt.Sprintf("/txs/%d", start)), scanTxsPage(start, min(esploraBlockTxsPageSize, txCount-start)))
	}
	return esplora
}

func TestScanBlockREST_ReadsEveryPageOfALargeBlock(t *testing.T) {
	const txCount = 87
	esplora := esploraBlockOf(t, txCount)

	transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)

	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != txCount {
		t.Fatalf("transfers %d, want one per tx (%d)", len(transfers), txCount)
	}
	seen := make(map[string]bool, txCount)
	for _, transfer := range transfers {
		seen[transfer.TxHash] = true
		if transfer.BlockHash != esploraTestBlockHash || transfer.BlockNumber != esploraTestHeight || transfer.Timestamp.Unix() != 1790907938 {
			t.Fatalf("transfer %+v", transfer)
		}
	}
	for i := 0; i < txCount; i++ {
		if !seen[scanTxID(i)] {
			t.Fatalf("tx %d (page %d) missing", i, i/esploraBlockTxsPageSize)
		}
	}
	for _, start := range []int{0, 25, 50, 75} {
		if esplora.hitCount(blockPath(fmt.Sprintf("/txs/%d", start))) != 1 {
			t.Fatalf("page %d not read exactly once", start)
		}
	}
	if esplora.hitCount(blockPath("/txs/100")) != 0 || esplora.hitCount(blockPath("/txs")) != 0 {
		t.Fatal("scanner read past tx_count or used the unpaginated endpoint")
	}
}

func TestScanBlockREST_ExactMultipleOfThePageSize(t *testing.T) {
	esplora := esploraBlockOf(t, 50)

	transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)

	if err != nil || len(transfers) != 50 || esplora.hitCount(blockPath("/txs/50")) != 0 {
		t.Fatalf("transfers %d err %v hits(50)=%d", len(transfers), err, esplora.hitCount(blockPath("/txs/50")))
	}
}

func TestScanBlockREST_CoinbaseOnlyBlock(t *testing.T) {
	esplora := esploraBlockOf(t, 1)

	transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)

	if err != nil || len(transfers) != 1 {
		t.Fatalf("transfers %d err %v", len(transfers), err)
	}
}

func TestScanBlockREST_RetriesARateLimitedPage(t *testing.T) {
	esplora := esploraBlockOf(t, 30)
	esplora.on(blockPath("/txs/25"),
		esploraAnswer{http.StatusTooManyRequests, "slow down"},
		esploraAnswer{http.StatusOK, scanTxsPage(25, 5)},
	)

	transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)

	if err != nil || len(transfers) != 30 {
		t.Fatalf("transfers %d err %v", len(transfers), err)
	}
}

func TestScanBlockREST_FailsInsteadOfReturningAPartialBlock(t *testing.T) {
	heightPath := fmt.Sprintf("%s/block-height/%d", esploraTestPrefix, esploraTestHeight)
	cases := map[string]func(*fakeEsplora){
		"block hash 404":        func(e *fakeEsplora) { e.on(heightPath, esploraAnswer{http.StatusNotFound, "Block not found"}) },
		"block hash not hex":    func(e *fakeEsplora) { e.ok(heightPath, "<html>") },
		"block header 500":      func(e *fakeEsplora) { e.on(blockPath(""), esploraAnswer{http.StatusInternalServerError, "boom"}) },
		"block header not json": func(e *fakeEsplora) { e.ok(blockPath(""), "oops") },
		"block header other hash": func(e *fakeEsplora) {
			e.ok(blockPath(""), `{"id":"`+strings.Repeat("1", 64)+`","height":154740,"tx_count":60}`)
		},
		"block header other height": func(e *fakeEsplora) {
			e.ok(blockPath(""), `{"id":"`+esploraTestBlockHash+`","height":1,"tx_count":60}`)
		},
		"block header without tx_count": func(e *fakeEsplora) { e.ok(blockPath(""), `{"id":"`+esploraTestBlockHash+`","height":154740}`) },
		"middle page 500": func(e *fakeEsplora) {
			e.on(blockPath("/txs/25"), esploraAnswer{http.StatusInternalServerError, "boom"})
		},
		"middle page 404": func(e *fakeEsplora) {
			e.on(blockPath("/txs/25"), esploraAnswer{http.StatusNotFound, "start index out of range"})
		},
		"middle page short": func(e *fakeEsplora) { e.ok(blockPath("/txs/25"), scanTxsPage(25, 24)) },
		"last page short":   func(e *fakeEsplora) { e.ok(blockPath("/txs/50"), scanTxsPage(50, 9)) },
		"last page empty":   func(e *fakeEsplora) { e.ok(blockPath("/txs/50"), "[]") },
		"page not json":     func(e *fakeEsplora) { e.ok(blockPath("/txs/0"), "{") },
		"page persistently rate limited": func(e *fakeEsplora) {
			e.on(blockPath("/txs/50"), esploraAnswer{http.StatusTooManyRequests, "slow down"})
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			esplora := esploraBlockOf(t, 60)
			breakIt(esplora)

			transfers, err := esplora.adapter(nil).ScanBlock(context.Background(), esploraTestHeight)

			if err == nil {
				t.Fatalf("expected an error, got %d transfers", len(transfers))
			}
			if transfers != nil {
				t.Fatalf("a failed scan must not return transfers, got %d", len(transfers))
			}
		})
	}
}

func TestEsploraErrorsDoNotLeakTheRPCURL(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	host := strings.TrimPrefix(closed.URL, "http://")
	closed.Close()
	live := newFakeEsplora(t).adapter(nil)
	live.cfg.RPCURL = "http://user:secret-key@" + host + "/secret-path/api"

	_, err := live.GetTransactionBlock(context.Background(), esploraTestTxID)

	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("err %v", err)
	}
}
