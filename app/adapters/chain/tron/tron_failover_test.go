package tron

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// tronEndpoint is one java-tron HTTP endpoint of a failover test: it answers block 0
// with genesis, every other call with answer, and records the paths and the API
// key header it received.
type tronEndpoint struct {
	server  *httptest.Server
	genesis string
	answer  func(path string) (int, string)

	mu    sync.Mutex
	paths []string
	keys  []string
}

func newTronEndpoint(t *testing.T, genesis string, answer func(path string) (int, string)) *tronEndpoint {
	t.Helper()
	endpoint := &tronEndpoint{genesis: genesis, answer: answer}
	endpoint.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		endpoint.mu.Lock()
		endpoint.paths = append(endpoint.paths, r.URL.Path)
		endpoint.keys = append(endpoint.keys, r.Header.Get(tronAPIKeyHeader))
		endpoint.mu.Unlock()
		if r.URL.Path == "/wallet/getblockbynum" && strings.Contains(string(body), `"num":0`) {
			_, _ = io.WriteString(w, `{"blockID":"`+endpoint.genesis+`","block_header":{"raw_data":{"timestamp":1529891469000}}}`)
			return
		}
		status, reply := endpoint.answer(r.URL.Path)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(endpoint.server.Close)
	return endpoint
}

func (e *tronEndpoint) calls(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, seen := range e.paths {
		if seen == path {
			count++
		}
	}
	return count
}

func (e *tronEndpoint) sawKey() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, key := range e.keys {
		if key != "" {
			return true
		}
	}
	return false
}

const tronTestHeadAnswer = `{"blockID":"` + tronTestHeadBlockID + `","block_header":{"raw_data":{"number":71532773,"timestamp":1791126834000}}}`

func tronAnswers(status int, body string) func(string) (int, string) {
	return func(string) (int, string) { return status, body }
}

// tronPaths answers each path its body, every other path "{}".
func tronPaths(bodies map[string]string) func(string) (int, string) {
	return func(path string) (int, string) {
		if body, ok := bodies[path]; ok {
			return http.StatusOK, body
		}
		return http.StatusOK, "{}"
	}
}

func nileGenesis() string { return models.TronGenesisBlockID(models.NetworkTronNile) }

// newTronFailoverAdapter is a Nile adapter whose primary is primaryURL and whose
// fallbacks are the endpoints, in order.
func newTronFailoverAdapter(primaryURL string, fallbacks ...*tronEndpoint) *TronLive {
	list := make([]chain.RPCFallback, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		list = append(list, chain.RPCFallback{URL: fallback.server.URL})
	}
	adapter := NewTronLive(TronConfig{
		ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, IsTestnet: true,
		RPCURL: primaryURL, RPCFallbacks: list, GenesisBlockID: nileGenesis(), APIKey: tronTestAPIKey,
	})
	adapter.retry.sleep = func(context.Context, time.Duration) error { return nil }
	return adapter
}

func TestTron_Failover_AReadMovesToAFallbackOfTheChainsNetwork(t *testing.T) {
	primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusBadGateway, "bad gateway"))
	fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronTestHeadAnswer))
	adapter := newTronFailoverAdapter(primary.server.URL, fallback)

	for i := 0; i < 2; i++ {
		head, err := adapter.GetLatestBlock(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint64(tronTestHeadNumber), head)
	}

	assert.Equal(t, 2, fallback.calls("/wallet/getblock"))
	assert.Equal(t, 1, fallback.calls("/wallet/getblockbynum"), "the fallback's network is asked once")
	assert.True(t, primary.sawKey(), "the API key goes to the primary")
	assert.False(t, fallback.sawKey(), "a fallback is another provider and never sees the key")
}

func TestTron_Failover_AFallbackOnAnotherNetworkIsNeverUsed(t *testing.T) {
	primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusBadGateway, "bad gateway"))
	mainnet := newTronEndpoint(t, models.TronGenesisBlockID(models.NetworkTronMainnet), tronAnswers(http.StatusOK, tronTestHeadAnswer))
	adapter := newTronFailoverAdapter(primary.server.URL, mainnet)

	_, err := adapter.GetLatestBlock(context.Background())

	require.Error(t, err)
	assert.Zero(t, mainnet.calls("/wallet/getblock"))
	assert.Zero(t, mainnet.calls("/wallet/getnowblock"))
}

func TestTron_Failover_AScanReadsItsBlockAndItsInfosFromOneEndpoint(t *testing.T) {
	node := newFakeTronNode(t)
	loadTronBlockFixtures(node, t)
	node.blocks[0] = []byte(`{"blockID":"` + nileGenesis() + `"}`)
	fallback := httptest.NewServer(node)
	t.Cleanup(fallback.Close)
	// The primary cannot serve the block and answers its infos empty, as a node
	// behind the block would.
	primary := newTronEndpoint(t, nileGenesis(), func(path string) (int, string) {
		if path == "/wallet/gettransactioninfobyblocknum" {
			return http.StatusOK, "{}"
		}
		return http.StatusBadGateway, "bad gateway"
	})
	adapter := NewTronLive(TronConfig{
		ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, IsTestnet: true, Tokens: []types.Token{tronTestNileUSDT},
		RPCURL: primary.server.URL, RPCFallbacks: []chain.RPCFallback{{URL: fallback.URL}}, GenesisBlockID: nileGenesis(),
	})

	transfers, err := adapter.ScanBlock(context.Background(), tronBlockWithUSDT)

	require.NoError(t, err)
	assert.Len(t, transfers, 8)
	assert.Zero(t, primary.calls("/wallet/gettransactioninfobyblocknum"), "the infos come from the endpoint that served the block")
}

func signedTestTRXTransfer(t *testing.T) *types.SignedTx {
	t.Helper()
	adapter, unsigned, key, _ := buildTestTRXTransfer(t)
	signed, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(key))
	require.NoError(t, err)
	return signed
}

func tronRefusal(code, message string) string {
	return `{"result":false,"code":"` + code + `","message":"` + hex.EncodeToString([]byte(message)) + `"}`
}

func TestTron_Broadcast_ClassifiesEveryAnswer(t *testing.T) {
	signed := signedTestTRXTransfer(t)
	accepted := `{"result":true,"txid":"` + signed.TxHash + `"}`

	duplicate := tronRefusal("DUP_TRANSACTION_ERROR", "dup transaction")
	known := `{"txID":"` + signed.TxHash + `","raw_data":{}}`
	t.Run("a duplicate an endpoint knows by its id was sent", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronPaths(map[string]string{tronBroadcastPath: duplicate}))
		fallback := newTronEndpoint(t, nileGenesis(), tronPaths(map[string]string{"/wallet/gettransactionbyid": known}))
		txID, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.NoError(t, err)
		assert.Equal(t, signed.TxHash, txID)
		assert.Equal(t, 1, primary.calls("/wallet/gettransactionbyid"))
		assert.Zero(t, fallback.calls(tronBroadcastPath))
	})
	t.Run("a duplicate no endpoint knows is unknown: the recent-id cache answers it after a refused push", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronPaths(map[string]string{tronBroadcastPath: duplicate}))
		fallback := newTronEndpoint(t, nileGenesis(), tronPaths(nil))
		_, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.ErrorIs(t, err, chain.ErrUnknownOutcome)
		assert.Equal(t, 1, fallback.calls("/wallet/gettransactionbyid"))
	})
	for _, refusal := range []struct{ code, message string }{
		{"NOT_ENOUGH_EFFECTIVE_CONNECTION", "P2P broadcast failed."},
		{"OTHER_ERROR", "class java.lang.NullPointerException"},
	} {
		code, message := refusal.code, refusal.message
		t.Run(code+" "+message+" may follow the push: unknown, not sent again", func(t *testing.T) {
			primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronRefusal(code, message)))
			fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, accepted))
			_, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
			require.ErrorIs(t, err, chain.ErrUnknownOutcome)
			assert.False(t, chain.RequestNotSent(err))
			assert.Zero(t, fallback.calls(tronBroadcastPath))
		})
	}
	t.Run("a refusal of the transaction is a known failure, never sent again", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronRefusal("SIGERROR", "validate signature error")))
		fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, accepted))
		_, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validate signature error")
		assert.NotErrorIs(t, err, chain.ErrUnknownOutcome)
		assert.False(t, chain.RequestNotSent(err))
		assert.Zero(t, fallback.calls("/wallet/broadcasthex"))
	})
	t.Run("a gateway failure may have relayed the bytes: unknown, not sent again", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusBadGateway, "bad gateway"))
		fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, accepted))
		_, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.ErrorIs(t, err, chain.ErrUnknownOutcome)
		assert.Zero(t, fallback.calls("/wallet/broadcasthex"))
	})
	t.Run("an answer that cannot be read is unknown", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, `{"result":tru`))
		_, err := newTronFailoverAdapter(primary.server.URL).BroadcastTransaction(context.Background(), signed)
		require.ErrorIs(t, err, chain.ErrUnknownOutcome)
	})
	t.Run("too few effective connections before the push passes the bytes to the next one", func(t *testing.T) {
		thin := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK,
			tronRefusal("NOT_ENOUGH_EFFECTIVE_CONNECTION", "Effective connection:0 lt minEffectiveConnection:1")))
		fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, accepted))
		txID, err := newTronFailoverAdapter(thin.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.NoError(t, err)
		assert.Equal(t, signed.TxHash, txID)
		assert.Equal(t, 1, fallback.calls(tronBroadcastPath))
	})
	t.Run("a node that did not take the bytes passes them to the next one", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		busy := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronRefusal("SERVER_BUSY", "Server busy")))
		fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, accepted))
		txID, err := newTronFailoverAdapter(closed.URL, busy, fallback).BroadcastTransaction(context.Background(), signed)
		require.NoError(t, err)
		assert.Equal(t, signed.TxHash, txID)
		assert.Equal(t, 1, busy.calls("/wallet/broadcasthex"))
		assert.Equal(t, 1, fallback.calls("/wallet/broadcasthex"))
	})
	t.Run("no node took the bytes: not sent", func(t *testing.T) {
		primary := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronRefusal("NO_CONNECTION", "No connection.")))
		fallback := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusTooManyRequests, "slow down"))
		_, err := newTronFailoverAdapter(primary.server.URL, fallback).BroadcastTransaction(context.Background(), signed)
		require.Error(t, err)
		assert.True(t, chain.RequestNotSent(err))
		assert.NotErrorIs(t, err, chain.ErrUnknownOutcome)
	})
}

func TestTron_ReplaceEndpoints_PointsLaterCallsAtTheNewList(t *testing.T) {
	old := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronTestHeadAnswer))
	moved := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, tronTestHeadAnswer))
	adapter := newTronFailoverAdapter(old.server.URL)

	adapter.ReplaceEndpoints(moved.server.URL, nil)
	_, err := adapter.GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, moved.calls("/wallet/getblock"))
	assert.Zero(t, old.calls("/wallet/getblock"))
	assert.Equal(t, moved.server.URL, adapter.Endpoint())

	adapter.ReplaceEndpoint(old.server.URL)
	assert.Equal(t, old.server.URL, adapter.Endpoint())
}

func TestTron_GenesisBlockID_IsTheIDOfBlockZero(t *testing.T) {
	endpoint := newTronEndpoint(t, nileGenesis(), tronAnswers(http.StatusOK, "{}"))
	id, err := GenesisBlockID(context.Background(), endpoint.server.URL)
	require.NoError(t, err)
	assert.Equal(t, nileGenesis(), id)

	empty := newTronEndpoint(t, "", tronAnswers(http.StatusOK, "{}"))
	_, err = GenesisBlockID(context.Background(), empty.server.URL)
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.Canceled))
}
