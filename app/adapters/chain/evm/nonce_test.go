package evm

import (
	"context"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestPending_Nonce_IsThePrimarysRaisedByAtMostOneByTheOthers(t *testing.T) {
	const sender = "0x1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name, other string
		want        uint64
	}{
		{"another endpoint holds a later broadcast", `"result":"0x6"`, 6},
		{"a raise is bounded", `"result":"0x9"`, 6},
		{"a lower answer changes nothing", `"result":"0x3"`, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := newCountingNode(t, map[string]string{"eth_getTransactionCount": `"result":"0x5"`})
			other := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_getTransactionCount": tc.other})
			adapter := sepoliaWithFallbacks(primary.server.URL, other.server.URL)

			nonce, err := adapter.pendingNonce(context.Background(), sender)
			require.NoError(t, err)
			assert.Equal(t, tc.want, nonce)
		})
	}
}

// rateLimitedOnceNode answers 429 to its first eth_getTransactionCount, then nonce.
func rateLimitedOnceNode(t *testing.T, nonce string) *httptest.Server {
	t.Helper()
	var limited atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if limited.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"`+nonce+`"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestPending_Nonce_ARateLimitedPrimaryWithFallbacksDownStillAnswers(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(down.Close)
	adapter := sepoliaWithFallbacks(rateLimitedOnceNode(t, "0x5").URL, down.URL)

	nonce, err := adapter.pendingNonce(context.Background(), "0x1111111111111111111111111111111111111111")
	require.NoError(t, err, "the primary is waited for, as with one endpoint, and a fallback that does not answer never fails the build")
	assert.Equal(t, uint64(5), nonce)
}

func TestPending_Nonce_DoesNotWaitForAHangingFallback(t *testing.T) {
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hanging.Close() })
	primary := newCountingNode(t, map[string]string{"eth_getTransactionCount": `"result":"0x5"`})
	adapter := sepoliaWithFallbacks(primary.server.URL, hanging.URL)

	started := time.Now()
	nonce, err := adapter.pendingNonce(context.Background(), "0x1111111111111111111111111111111111111111")
	require.NoError(t, err)
	assert.Equal(t, uint64(5), nonce)
	assert.Less(t, time.Since(started), 2*time.Second, "the other endpoints get a short budget")
}

// signedSepoliaTransfer is a real signed transfer of nonce from a fresh key.
func signedSepoliaTransfer(t *testing.T, nonce uint64) (*types.SignedTx, string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	tx := gethtypes.NewTransaction(nonce, common.HexToAddress("0x2222222222222222222222222222222222222222"), big.NewInt(1), 21_000, big.NewInt(1_000_000_000), nil)
	signed, err := gethtypes.SignTx(tx, gethtypes.LatestSignerForChainID(big.NewInt(models.EVMNetworkIDEthereumSepolia)), key)
	require.NoError(t, err)
	raw, err := signed.MarshalBinary()
	require.NoError(t, err)
	return &types.SignedTx{RawBytes: raw, TxHash: signed.Hash().Hex()}, crypto.PubkeyToAddress(key.PublicKey).Hex()
}

func TestPending_Nonce_NeverGoesBelowTheLastBroadcastOfThisProcess(t *testing.T) {
	signed, sender := signedSepoliaTransfer(t, 9)
	lagging := newCountingNode(t, map[string]string{
		"eth_sendRawTransaction":  `"result":"` + signed.TxHash + `"`,
		"eth_getTransactionCount": `"result":"0x9"`,
	})
	adapter := sepoliaWithFallbacks(lagging.server.URL)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	adapter.nonces.now = func() time.Time { return now }

	hash, err := adapter.BroadcastTransaction(context.Background(), signed)
	require.NoError(t, err)
	require.Equal(t, signed.TxHash, hash)

	nonce, err := adapter.pendingNonce(context.Background(), sender)
	require.NoError(t, err)
	assert.Equal(t, uint64(10), nonce, "the node does not show nonce 9 yet; this process broadcast it")

	shared := adapter.WithFeePolicy(adapter.FeePolicy()).(*EVMLive)
	nonce, err = shared.pendingNonce(context.Background(), sender)
	require.NoError(t, err)
	assert.Equal(t, uint64(10), nonce, "an adapter copy for another fee policy shares the floor")

	now = now.Add(nonceFloorTTL + time.Second)
	nonce, err = adapter.pendingNonce(context.Background(), sender)
	require.NoError(t, err)
	assert.Equal(t, uint64(9), nonce, "after the floor's lifetime the nodes decide again (a dropped transaction is not waited for forever)")
}

func TestBroadcast_ReturnsTheSignedHashWhenTheNodeAnswersAnother(t *testing.T) {
	signed, _ := signedSepoliaTransfer(t, 3)
	odd := newCountingNode(t, map[string]string{
		"eth_sendRawTransaction": `"result":"0x00000000000000000000000000000000000000000000000000000000000000ff"`,
	})
	adapter := sepoliaWithFallbacks(odd.server.URL)

	hash, err := adapter.BroadcastTransaction(context.Background(), signed)
	require.NoError(t, err)
	assert.Equal(t, signed.TxHash, hash, "the hash of the signed bytes is the transaction's")
}
