package xrp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// rippledEndpoint is one rippled JSON-RPC endpoint of a failover test: server_info
// reports networkID (altnet reserves, validated ledger 100), every other method is
// answered by answer. It records the methods it received.
type rippledEndpoint struct {
	server    *httptest.Server
	networkID string
	answer    func(method string) (int, string)

	mu      sync.Mutex
	methods []string
}

func newRippledEndpoint(t *testing.T, networkID string, answer func(method string) (int, string)) *rippledEndpoint {
	t.Helper()
	endpoint := &rippledEndpoint{networkID: networkID, answer: answer}
	endpoint.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		endpoint.mu.Lock()
		endpoint.methods = append(endpoint.methods, req.Method)
		endpoint.mu.Unlock()
		if req.Method == "server_info" {
			_, _ = fmt.Fprintf(w, `{"result":{"status":"success","info":{"network_id":%s,"validated_ledger":{"reserve_base_xrp":1,"reserve_inc_xrp":0.2,"seq":100}}}}`, endpoint.networkID)
			return
		}
		status, reply := endpoint.answer(req.Method)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(endpoint.server.Close)
	return endpoint
}

func (e *rippledEndpoint) calls(method string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, seen := range e.methods {
		if seen == method {
			count++
		}
	}
	return count
}

func rippled(status int, body string) func(string) (int, string) {
	return func(string) (int, string) { return status, body }
}

const validatedLedger42 = `{"result":{"status":"success","ledger":{"ledger_index":42}}}`

func rippledError(code string) string {
	return `{"result":{"status":"error","error":"` + code + `"}}`
}

// newAltnetFailoverAdapter is an altnet adapter whose primary is primaryURL and
// whose fallbacks are the endpoints, in order.
func newAltnetFailoverAdapter(primaryURL string, fallbacks ...*rippledEndpoint) *Live {
	list := make([]chain.RPCFallback, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		list = append(list, chain.RPCFallback{URL: fallback.server.URL})
	}
	return NewLive(Config{
		ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, IsTestnet: true,
		RPCURL: primaryURL, RPCFallbacks: list, NetworkID: models.XRPLNetworkID(models.NetworkXRPLTestnet),
	})
}

func TestXRP_Failover_AReadMovesToAFallbackOfTheChainsNetwork(t *testing.T) {
	primary := newRippledEndpoint(t, "1", rippled(http.StatusBadGateway, "bad gateway"))
	fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, validatedLedger42))
	adapter := newAltnetFailoverAdapter(primary.server.URL, fallback)

	for i := 0; i < 2; i++ {
		index, err := adapter.GetLatestBlock(context.Background())
		require.NoError(t, err)
		assert.Equal(t, uint64(42), index)
	}
	assert.Equal(t, 2, fallback.calls("ledger"))
	assert.Equal(t, 1, fallback.calls("server_info"), "the fallback's network is asked once")
}

func TestXRP_Failover_AFallbackOnAnotherNetworkIsNeverUsed(t *testing.T) {
	primary := newRippledEndpoint(t, "1", rippled(http.StatusBadGateway, "bad gateway"))
	mainnet := newRippledEndpoint(t, "0", rippled(http.StatusOK, validatedLedger42))
	adapter := newAltnetFailoverAdapter(primary.server.URL, mainnet)

	_, err := adapter.GetLatestBlock(context.Background())

	require.Error(t, err)
	assert.Zero(t, mainnet.calls("ledger"))
}

func TestXRP_Failover_ANodeThatCannotAnswerIsLeftAndAnAnswerIsKept(t *testing.T) {
	for _, code := range []string{"noNetwork", "tooBusy", "slowDown", "lgrNotFound"} {
		t.Run(code+" moves on", func(t *testing.T) {
			primary := newRippledEndpoint(t, "1", rippled(http.StatusOK, rippledError(code)))
			fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, validatedLedger42))
			index, err := newAltnetFailoverAdapter(primary.server.URL, fallback).GetLatestBlock(context.Background())
			require.NoError(t, err)
			assert.Equal(t, uint64(42), index)
		})
	}
	t.Run("an account not found is the answer", func(t *testing.T) {
		primary := newRippledEndpoint(t, "1", rippled(http.StatusOK, rippledError("actNotFound")))
		fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, `{"result":{"status":"success","account_data":{"Balance":"5"}}}`))
		balance, err := newAltnetFailoverAdapter(primary.server.URL, fallback).GetBalance(context.Background(), xrpTestClassic)
		require.NoError(t, err)
		assert.Zero(t, balance.Amount.Sign())
		assert.Zero(t, fallback.calls("account_info"))
	})
}

// signedAltnetPayment builds and signs a 1 XRP payment against a local altnet
// server; nothing is submitted.
func signedAltnetPayment(t *testing.T) *types.SignedTx {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	from, err := addressing.DeriveXRPAddress(key.PubKey().SerializeCompressed())
	require.NoError(t, err)
	builder := newRippledEndpoint(t, "1", func(method string) (int, string) {
		switch method {
		case "fee":
			return http.StatusOK, `{"result":{"status":"success","drops":{"open_ledger_fee":"10","minimum_fee":"10"}}}`
		case "account_info":
			return http.StatusOK, `{"result":{"status":"success","account_data":{"Balance":"10000000","Sequence":7}}}`
		}
		t.Errorf("unexpected rpc %s while building", method)
		return http.StatusBadRequest, "no"
	})
	adapter := NewLive(Config{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, RPCURL: builder.server.URL, IsTestnet: true})
	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{
		From: from, To: "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe", Amount: big.NewInt(1_000_000), Asset: models.NativeXRP,
	})
	require.NoError(t, err)
	signed, err := adapter.SignTransaction(context.Background(), unsigned, key.Serialize())
	require.NoError(t, err)
	return signed
}

func engineResult(code string) string {
	return `{"result":{"status":"success","engine_result":"` + code + `"}}`
}

func TestXRP_Broadcast_ClassifiesEveryAnswer(t *testing.T) {
	signed := signedAltnetPayment(t)
	submit := func(t *testing.T, primary *rippledEndpoint, fallbacks ...*rippledEndpoint) (string, error) {
		t.Helper()
		return newAltnetFailoverAdapter(primary.server.URL, fallbacks...).BroadcastTransaction(context.Background(), signed)
	}

	for _, code := range []string{"tesSUCCESS", "terQUEUED", "tefALREADY", "tecUNFUNDED_PAYMENT"} {
		t.Run(code+" was sent", func(t *testing.T) {
			hash, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult(code))))
			require.NoError(t, err)
			assert.True(t, strings.EqualFold(signed.TxHash, hash))
		})
	}
	for _, code := range []string{"temBAD_FEE", "tefBAD_AUTH"} {
		t.Run(code+" is a known failure", func(t *testing.T) {
			fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult("tesSUCCESS")))
			_, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult(code))), fallback)
			require.Error(t, err)
			assert.Contains(t, err.Error(), code)
			assert.NotErrorIs(t, err, chain.ErrUnknownOutcome)
			assert.False(t, chain.RequestNotSent(err))
			assert.Zero(t, fallback.calls("submit"))
		})
	}
	for _, code := range []string{"tefPAST_SEQ", "tefMAX_LEDGER", "terPRE_SEQ"} {
		t.Run(code+" may have applied or may still apply: unknown", func(t *testing.T) {
			_, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult(code))))
			require.ErrorIs(t, err, chain.ErrUnknownOutcome)
		})
	}
	t.Run("a request refused as invalid is a known failure", func(t *testing.T) {
		_, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusOK, rippledError("invalidTransaction"))))
		require.Error(t, err)
		assert.NotErrorIs(t, err, chain.ErrUnknownOutcome)
		assert.False(t, chain.RequestNotSent(err))
	})
	t.Run("a gateway failure may have relayed the bytes: unknown, not sent again", func(t *testing.T) {
		fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult("tesSUCCESS")))
		_, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusBadGateway, "bad gateway")), fallback)
		require.ErrorIs(t, err, chain.ErrUnknownOutcome)
		assert.Zero(t, fallback.calls("submit"))
	})
	t.Run("an answer that cannot be read is unknown", func(t *testing.T) {
		_, err := submit(t, newRippledEndpoint(t, "1", rippled(http.StatusOK, `{"result":{"status":"succ`)))
		require.ErrorIs(t, err, chain.ErrUnknownOutcome)
	})
	t.Run("a server that did not relay the bytes passes them to the next one", func(t *testing.T) {
		local := newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult("telINSUF_FEE_P")))
		busy := newRippledEndpoint(t, "1", rippled(http.StatusOK, rippledError("tooBusy")))
		fallback := newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult("tesSUCCESS")))
		hash, err := submit(t, local, busy, fallback)
		require.NoError(t, err)
		assert.True(t, strings.EqualFold(signed.TxHash, hash))
		assert.Equal(t, 1, busy.calls("submit"))
		assert.Equal(t, 1, fallback.calls("submit"))
	})
	t.Run("no server took the bytes: not sent", func(t *testing.T) {
		local := newRippledEndpoint(t, "1", rippled(http.StatusOK, engineResult("telCAN_NOT_QUEUE")))
		limited := newRippledEndpoint(t, "1", rippled(http.StatusTooManyRequests, "slow down"))
		_, err := submit(t, local, limited)
		require.Error(t, err)
		assert.True(t, chain.RequestNotSent(err))
		assert.NotErrorIs(t, err, chain.ErrUnknownOutcome)
	})
}

func TestXRP_NetworkID_IsTheServersNetworkID(t *testing.T) {
	altnet := newRippledEndpoint(t, "1", rippled(http.StatusOK, "{}"))
	id, err := NetworkID(context.Background(), altnet.server.URL)
	require.NoError(t, err)
	assert.Equal(t, "1", id)

	silent := newRippledEndpoint(t, `null`, rippled(http.StatusOK, "{}"))
	_, err = NetworkID(context.Background(), silent.server.URL)
	require.Error(t, err, "a server that does not say its network is not taken for mainnet")
}

func TestXRP_ReplaceEndpoints_PointsLaterReadsAtTheNewList(t *testing.T) {
	old := newRippledEndpoint(t, "1", rippled(http.StatusOK, validatedLedger42))
	moved := newRippledEndpoint(t, "1", rippled(http.StatusOK, validatedLedger42))
	adapter := newAltnetFailoverAdapter(old.server.URL)

	adapter.ReplaceEndpoints(moved.server.URL, nil)
	_, err := adapter.GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, moved.calls("ledger"))
	assert.Zero(t, old.calls("ledger"))
}
