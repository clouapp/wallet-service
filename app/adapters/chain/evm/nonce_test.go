package evm

import (
	"context"
	"math/big"
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

func TestPending_Nonce_IsTheHighestTheReadyEndpointsAnswer(t *testing.T) {
	primary := newCountingNode(t, map[string]string{"eth_getTransactionCount": `"result":"0x5"`})
	ahead := newCountingNode(t, map[string]string{"eth_chainId": sepoliaChainIDResult, "eth_getTransactionCount": `"result":"0x7"`})
	adapter := sepoliaWithFallbacks(primary.server.URL, ahead.server.URL)

	nonce, err := adapter.pendingNonce(context.Background(), "0x1111111111111111111111111111111111111111")
	require.NoError(t, err)
	assert.Equal(t, uint64(7), nonce, "a node that has the last broadcast in its mempool knows the next nonce")
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
