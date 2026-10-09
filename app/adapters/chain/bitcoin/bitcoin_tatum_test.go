package bitcoin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	tatumTestKey        = "t-0123456789abcdef-SECRET"
	tatumTestAddress    = failoverTestAddress
	tatumConfirmedTx    = failoverTestUTXOTx
	tatumConfirmedValue = 49859
)

var (
	tatumUnconfirmedTx = strings.Repeat("1", 64)
	tatumSpentTx       = strings.Repeat("2", 64)
)

// fakeTatum is the gateway (bitcoind JSON-RPC) and the Data API of one test.
type fakeTatum struct {
	t *testing.T

	mu             sync.Mutex
	gatewayKeys    []string
	dataKeys       []string
	dataQueries    []string
	gatewayMethods []string

	genesis       string
	dataStatus    int
	dataBody      string
	gatewayStatus int
	gatewayBody   string
	// heldValue is what gettxout answers for the confirmed output, in LTC.
	heldValue string
}

func newFakeTatum(t *testing.T) (*fakeTatum, *httptest.Server, *httptest.Server) {
	fake := &fakeTatum{t: t, genesis: ltcTestnetGenesisHash, dataStatus: http.StatusOK, heldValue: "0.00049859"}
	fake.dataBody = `[` +
		`{"chain":"litecoin-testnet","address":"` + tatumTestAddress + `","txHash":"` + tatumConfirmedTx + `","index":1,"value":0.00049859,"valueAsString":"0.00049859"},` +
		`{"chain":"litecoin-testnet","address":"` + tatumTestAddress + `","txHash":"` + tatumUnconfirmedTx + `","index":0,"value":7e-8,"valueAsString":"0.00000007"},` +
		`{"chain":"litecoin-testnet","address":"` + tatumTestAddress + `","txHash":"0x` + tatumSpentTx + `","index":2,"value":0.001,"valueAsString":"0.001"}]`
	gateway := httptest.NewServer(http.HandlerFunc(fake.serveGateway))
	data := httptest.NewServer(http.HandlerFunc(fake.serveData))
	t.Cleanup(gateway.Close)
	t.Cleanup(data.Close)
	return fake, gateway, data
}

func (f *fakeTatum) serveGateway(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Errorf("gateway request: %v", err)
	}
	f.mu.Lock()
	f.gatewayKeys = append(f.gatewayKeys, r.Header.Get(apiKeyHeader))
	f.gatewayMethods = append(f.gatewayMethods, req.Method)
	status, failure, genesis, heldValue := f.gatewayStatus, f.gatewayBody, f.genesis, f.heldValue
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, failure)
		return
	}
	result := `null`
	switch req.Method {
	case "getblockhash":
		result = `"` + genesis + `"`
	case "getblockcount":
		result = `4907461`
	case "listunspent":
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found: listunspent"}}`)
		return
	case "gettxout":
		var txID string
		_ = json.Unmarshal(req.Params[0], &txID)
		switch txID {
		case tatumConfirmedTx:
			result = `{"confirmations":16,"value":` + heldValue + `,"scriptPubKey":{"address":"` + tatumTestAddress + `"}}`
		case tatumUnconfirmedTx:
			result = `{"confirmations":0,"value":0.00000007,"scriptPubKey":{"address":"` + tatumTestAddress + `"}}`
		}
	}
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+result+`}`)
}

// recorded is a copy of what the servers received.
func (f *fakeTatum) recorded() (gatewayKeys, dataKeys, dataQueries, gatewayMethods []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.gatewayKeys...), append([]string(nil), f.dataKeys...),
		append([]string(nil), f.dataQueries...), append([]string(nil), f.gatewayMethods...)
}

// configure changes the answers while the servers run.
func (f *fakeTatum) configure(change func(*fakeTatum)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeTatum) serveData(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.dataKeys = append(f.dataKeys, r.Header.Get(apiKeyHeader))
	f.dataQueries = append(f.dataQueries, r.URL.Path+"?"+r.URL.RawQuery)
	status, body := f.dataStatus, f.dataBody
	f.mu.Unlock()
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// newTatumTestLive is a tltc adapter whose primary is down and whose only fallback
// is the fake Tatum (built as fallbackProvider builds a *.tatum.io one).
func newTatumTestLive(t *testing.T, gatewayURL, dataURL, apiKey string) *BitcoinLive {
	t.Helper()
	primary, primarySrv := newFailoverEsplora(t)
	primary.down.Store(true)
	live := newFailoverTestLive(t, primarySrv.URL)
	data, err := newTatumDataAPI(dataURL, apiKey, live.network.name, httpclient.New(bitcoinRESTTimeout))
	if err != nil {
		t.Fatal(err)
	}
	gateway := newDirectProvider(live.secondary(gatewayURL, apiKey), "fallback-1", true)
	live.providers = newBitcoinFailover(live.cfg.ChainIDStr, live.providers.members[0].provider, newTatumProvider(gateway, data))
	return live
}

func TestTatumFallback_KeylessLeavesUTXOsToOtherProviders(t *testing.T) {
	fake, gateway, data := newFakeTatum(t)
	live := newTatumTestLive(t, gateway.URL, data.URL, "")
	ctx := context.Background()

	if _, err := live.listConfirmedUTXOs(ctx, tatumTestAddress); !errors.Is(err, errProviderUnsupported) {
		t.Fatalf("utxos err = %v, want unsupported", err)
	}
	if _, err := live.GetBalance(ctx, tatumTestAddress); !errors.Is(err, errProviderUnsupported) {
		t.Fatalf("balance err = %v, want unsupported", err)
	}
	if tip, err := live.GetLatestBlock(ctx); err != nil || tip != 4907461 {
		t.Fatalf("tip = %d, %v", tip, err)
	}
	gatewayKeys, _, dataQueries, _ := fake.recorded()
	if len(dataQueries) != 0 {
		t.Fatalf("keyless Data API calls: %v", dataQueries)
	}
	for _, key := range gatewayKeys {
		if key != "" {
			t.Fatalf("keyless gateway got %s %q", apiKeyHeader, key)
		}
	}
	if tatum := live.providers.members[1]; tatum.consecutiveFailures != 0 {
		t.Fatalf("unsupported calls blamed Tatum %d times", tatum.consecutiveFailures)
	}
}

func TestTatumFallback_KeyReadsVerifiedUTXOsFromTheDataAPI(t *testing.T) {
	fake, gateway, data := newFakeTatum(t)
	live := newTatumTestLive(t, gateway.URL, data.URL, tatumTestKey)
	ctx := context.Background()

	utxos, err := live.listConfirmedUTXOs(ctx, tatumTestAddress)
	if err != nil {
		t.Fatalf("utxos: %v", err)
	}
	if len(utxos) != 1 || utxos[0].TxID != tatumConfirmedTx || utxos[0].Vout != 1 || utxos[0].Value != tatumConfirmedValue {
		t.Fatalf("utxos = %+v, want only the confirmed, unspent output", utxos)
	}
	balance, err := live.GetBalance(ctx, tatumTestAddress)
	if err != nil || balance.Amount.Int64() != tatumConfirmedValue+7+100_000 || balance.Human != "0.00149866" {
		t.Fatalf("balance = %+v, %v; want every listed UTXO", balance, err)
	}

	gatewayKeys, dataKeys, dataQueries, _ := fake.recorded()
	if len(dataQueries) != 2 {
		t.Fatalf("data api calls = %v", dataQueries)
	}
	for _, query := range dataQueries {
		for _, want := range []string{tatumUTXOsPath + "?", "chain=litecoin-testnet", "address=" + tatumTestAddress, "totalValue=" + tatumUTXOTotalValueAll} {
			if !strings.Contains(query, want) {
				t.Errorf("data api query %q misses %q", query, want)
			}
		}
		if strings.Contains(query, tatumTestKey) {
			t.Errorf("key in the query string: %q", query)
		}
	}
	for _, keys := range [][]string{dataKeys, gatewayKeys} {
		for _, key := range keys {
			if key != tatumTestKey {
				t.Fatalf("%s = %q, want the configured key on every Tatum call", apiKeyHeader, key)
			}
		}
	}
}

func TestTatumFallback_NodeDisagreeingWithTheDataAPIIsAnError(t *testing.T) {
	fake, gateway, data := newFakeTatum(t)
	fake.configure(func(f *fakeTatum) { f.heldValue = "0.00049858" })
	live := newTatumTestLive(t, gateway.URL, data.URL, tatumTestKey)

	_, err := live.listConfirmedUTXOs(context.Background(), tatumTestAddress)
	if err == nil || !strings.Contains(err.Error(), "the node holds 49858 sats") {
		t.Fatalf("err = %v, want the value mismatch", err)
	}
}

func TestTatumFallback_KeyNeverReachesErrorsOrLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	fake, gateway, data := newFakeTatum(t)
	echo := `{"statusCode":401,"errorCode":"subscription.invalid","message":"Unable to find valid subscription for '` + tatumTestKey + `'"}`
	fake.configure(func(f *fakeTatum) {
		f.dataStatus, f.dataBody = http.StatusUnauthorized, echo
		f.gatewayStatus, f.gatewayBody = http.StatusUnauthorized, echo
	})
	live := newTatumTestLive(t, gateway.URL, data.URL, tatumTestKey)
	ctx := context.Background()

	_, utxoErr := live.listConfirmedUTXOs(ctx, tatumTestAddress)
	_, tipErr := live.GetLatestBlock(ctx)
	fake.configure(func(f *fakeTatum) { f.gatewayStatus = 0 })
	_, dataErr := live.GetBalance(ctx, tatumTestAddress)
	for name, err := range map[string]error{"utxos": utxoErr, "tip": tipErr, "balance": dataErr} {
		if err == nil {
			t.Fatalf("%s: want an error", name)
		}
		if strings.Contains(err.Error(), tatumTestKey) {
			t.Fatalf("%s error leaks the key: %v", name, err)
		}
		// Since b0e2b3d a gateway failure is the provider sentinel; only the Data API
		// path still names the upstream status.
		if name == "balance" {
			if !strings.Contains(err.Error(), "401") {
				t.Errorf("%s error %q does not say 401", name, err)
			}
		} else if !errors.Is(err, chain.ErrProvider) {
			t.Errorf("%s error %q is not the provider sentinel", name, err)
		}
	}
	if !strings.Contains(dataErr.Error(), redactedSecret) {
		t.Errorf("balance error %q: want the echoed key redacted", dataErr)
	}
	if strings.Contains(logs.String(), tatumTestKey) {
		t.Fatalf("logs leak the key:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "btc provider failed") {
		t.Fatalf("expected failover logs, got:\n%s", logs.String())
	}
}

func TestTatumFallback_KeyGoesToTatumHostsOnly(t *testing.T) {
	_, other := newFailoverEsplora(t)
	live := NewBitcoinLive(BitcoinConfig{
		ChainIDStr: models.ChainLTC, NativeSymbol: models.NativeLTC, RPCURL: "https://litecoinspace.org/api",
		Fallbacks: []BitcoinFallback{
			{URL: other.URL, APIKey: tatumTestKey},
			{URL: "https://ltc.example.com/rpc", APIKey: tatumTestKey},
			{URL: ltcMainnetTatumGateway, APIKey: tatumTestKey},
		},
	})
	if got := len(live.providers.members); got != 4 {
		t.Fatalf("members = %d, want primary + 3", got)
	}
	for _, member := range live.providers.members[1:3] {
		direct := member.provider.(directProvider)
		if direct.live.apiKey != "" || direct.live.rpc.Header(apiKeyHeader) != "" {
			t.Fatalf("%s got the Tatum key", direct.label())
		}
	}
	tatum, ok := live.providers.members[3].provider.(tatumProvider)
	if !ok || tatum.label() != "fallback-3 (tatum)" {
		t.Fatalf("provider = %#v, want the Tatum one", live.providers.members[3].provider)
	}
	if tatum.gateway.live.rpc.Header(apiKeyHeader) != tatumTestKey || tatum.data == nil {
		t.Fatal("Tatum gateway without its key or Data API")
	}
	if tatum.data.baseURL != DefaultTatumDataAPIURL || tatum.data.chain != "litecoin" {
		t.Fatalf("data api = %s %s, want the default URL and litecoin", tatum.data.baseURL, tatum.data.chain)
	}
}

func TestTatumDataAPI_Configuration(t *testing.T) {
	client := http.DefaultClient
	if data, err := newTatumDataAPI("", "", models.NetworkLitecoinMainnet, client); data != nil || err != nil {
		t.Fatalf("keyless = %v, %v; want no Data API", data, err)
	}
	if data, err := newTatumDataAPI("", tatumTestKey, models.NetworkBitcoinTestnet4, client); data != nil || err != nil {
		t.Fatalf("testnet4 = %v, %v; want no Data API (not indexed)", data, err)
	}
	data, err := newTatumDataAPI("https://tatum-proxy.example.com/", tatumTestKey, models.NetworkLitecoinMainnet, client)
	if err != nil || data.baseURL != "https://tatum-proxy.example.com" {
		t.Fatalf("custom URL = %+v, %v", data, err)
	}
	if _, err := newTatumDataAPI("http://tatum-proxy.example.com", tatumTestKey, models.NetworkLitecoinMainnet, client); err == nil {
		t.Fatal("plain http to a remote host must be refused: the key would travel in clear")
	}
	if !isTatumURL(ltcMainnetTatumGateway) || isTatumURL("http://litecoin-mainnet.gateway.tatum.io") || isTatumURL("https://tatum.io.example.com") {
		t.Fatal("isTatumURL must accept https *.tatum.io hosts only")
	}
}

func TestDirectProvider_MethodNotFoundIsUnsupportedNotAFailure(t *testing.T) {
	fake, gateway, _ := newFakeTatum(t)
	primary, primarySrv := newFailoverEsplora(t)
	primary.down.Store(true)
	live := newFailoverTestLive(t, primarySrv.URL)
	node := newDirectProvider(live.secondary(gateway.URL, ""), "fallback-1", true)
	live.providers = newBitcoinFailover(live.cfg.ChainIDStr, live.providers.members[0].provider, node)

	for range btcProviderFailuresToOpen + 1 {
		if _, err := live.GetBalance(context.Background(), tatumTestAddress); !errors.Is(err, errProviderUnsupported) {
			t.Fatalf("balance err = %v, want unsupported", err)
		}
	}
	if member := live.providers.members[1]; member.consecutiveFailures != 0 || member.trips != 0 {
		t.Fatalf("method not found counted as failures (%d, trips %d)", member.consecutiveFailures, member.trips)
	}
	_, _, _, methods := fake.recorded()
	if !strings.Contains(strings.Join(methods, ","), "listunspent") {
		t.Fatalf("gateway methods = %v", methods)
	}
}

func TestDefaultBitcoinFallbackURLs(t *testing.T) {
	cases := []struct {
		cfg  BitcoinConfig
		want int
	}{
		{BitcoinConfig{ChainIDStr: models.ChainLTC}, 5},
		{BitcoinConfig{ChainIDStr: models.ChainLTC, IsTestnet: true}, 3},
		{BitcoinConfig{ChainIDStr: models.ChainTLTC}, 3},
		{BitcoinConfig{ChainIDStr: models.ChainBTC}, 0},
		{BitcoinConfig{ChainIDStr: models.ChainTBTC}, 0},
	}
	for _, tc := range cases {
		urls := DefaultBitcoinFallbackURLs(tc.cfg)
		if len(urls) != tc.want {
			t.Fatalf("%s testnet=%v: %d defaults, want %d", tc.cfg.ChainIDStr, tc.cfg.IsTestnet, len(urls), tc.want)
		}
		cfg := tc.cfg
		for _, rawURL := range urls {
			cfg.Fallbacks = append(cfg.Fallbacks, BitcoinFallback{URL: rawURL})
		}
		if members := len(NewBitcoinLive(cfg).providers.members); members != tc.want+1 {
			t.Fatalf("%s: %d providers built from %d defaults; one was refused", cfg.ChainIDStr, members-1, tc.want)
		}
	}
	mainnet := DefaultBitcoinFallbackURLs(BitcoinConfig{ChainIDStr: models.ChainLTC})
	if last := mainnet[len(mainnet)-1]; last != ltcMainnetTatumGateway {
		t.Fatalf("Tatum must come last on mainnet, got %s", last)
	}
	mainnet[0] = "changed"
	if DefaultBitcoinFallbackURLs(BitcoinConfig{ChainIDStr: models.ChainLTC})[0] == "changed" {
		t.Fatal("defaults must be returned as a copy")
	}
}

func TestBitcoinRPC_AcceptsBlocksAboveTheDefaultLimit(t *testing.T) {
	const defaultRPCLimit = 1 << 20
	big := `{"jsonrpc":"2.0","id":1,"result":"` + strings.Repeat("a", defaultRPCLimit) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, big) }))
	t.Cleanup(srv.Close)
	var out string

	err := rpc.NewRPCClient(rpc.RPCClientDeps{URL: srv.URL}).Call(context.Background(), "getblock", &out)
	if err == nil || !strings.Contains(err.Error(), "response larger than") {
		t.Fatalf("default client err = %v, want an explicit size error", err)
	}
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainLTC, RPCURL: srv.URL})
	if err := live.rpc.Call(context.Background(), "getblock", &out); err != nil || len(out) != defaultRPCLimit {
		t.Fatalf("bitcoin client: %d bytes, %v", len(out), err)
	}
	fallback := live.secondary(srv.URL, "")
	if err := fallback.rpc.Call(context.Background(), "getblock", &out); err != nil {
		t.Fatalf("bitcoin fallback client: %v", err)
	}
}
