package solana

import (
	"context"
	"crypto/ed25519"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	_ chain.TransactionExpiryChecker = (*SolanaLive)(nil)
	_ interface {
		LastValidBlock(*types.SignedTx) (uint64, error)
	} = (*SolanaLive)(nil)
)

const (
	expiryBlockhash = "EkSnNWid2cvwEVnVx9aBqawnmiCNiDgp3gUdkDPTKN1N"
	expiryPayer     = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"
	expiryReceiver  = "4Nd1mBQtrMJVYVfKf2PJy9NZUZdTAsp7D4xWLs4gDB4T"
)

// signedExpiryTransfer builds and signs a native transfer whose recent blockhash
// the node answered with lastValidBlockHeight 500.
func signedExpiryTransfer(t *testing.T, adapter *SolanaLive) *types.SignedTx {
	t.Helper()
	unsigned, err := adapter.buildSolanaTransfer(context.Background(), types.TransferRequest{
		From: expiryPayer, To: expiryReceiver, Amount: big.NewInt(1),
	})
	require.NoError(t, err)
	message, _, err := adapter.SolanaSigningView(unsigned)
	require.NoError(t, err)
	// A signature that does not verify is enough for these reads: AssembleSolana
	// checks it, so build the signed bytes by hand.
	return &types.SignedTx{RawBytes: append(append([]byte{1}, make([]byte, ed25519.SignatureSize)...), message...), TxHash: "sig"}
}

func TestSolana_LastValidBlock_IsTheHeightTheNodeGaveTheBlockhash(t *testing.T) {
	node := newCountingSolanaNode(t, map[string]string{
		"getLatestBlockhash": `"result":{"context":{"slot":1},"value":{"blockhash":"` + expiryBlockhash + `","lastValidBlockHeight":500}}`,
	})
	adapter := devnetWithFallbacks(node.server.URL)
	signed := signedExpiryTransfer(t, adapter)

	last, err := adapter.LastValidBlock(signed)
	require.NoError(t, err)
	assert.Equal(t, uint64(500), last)

	other := devnetWithFallbacks(node.server.URL)
	last, err = other.LastValidBlock(signed)
	require.NoError(t, err)
	assert.Zero(t, last, "a blockhash this process did not fetch has no known height: nothing is recorded")
	_, err = adapter.LastValidBlock(&types.SignedTx{RawBytes: []byte{0x01}})
	assert.Error(t, err)
}

func TestSolana_TransactionExpired_OnlyPastItsHeightAndUnknownToEveryEndpoint(t *testing.T) {
	unknown := `"result":{"context":{"slot":900},"value":[null]}`
	known := `"result":{"context":{"slot":900},"value":[{"slot":450,"confirmations":null,"err":null,"confirmationStatus":"finalized"}]}`
	for _, tc := range []struct {
		name            string
		height          string
		primary, others string
		expired         bool
	}{
		{"still valid", `"result":500`, unknown, unknown, false},
		{"past its height and unknown everywhere", `"result":501`, unknown, unknown, true},
		{"past its height but another endpoint has it", `"result":501`, unknown, known, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := newCountingSolanaNode(t, map[string]string{"getBlockHeight": tc.height, "getSignatureStatuses": tc.primary})
			fallback := newCountingSolanaNode(t, map[string]string{"getGenesisHash": devnetGenesisResult, "getBlockHeight": tc.height, "getSignatureStatuses": tc.others})
			adapter := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

			expired, err := adapter.TransactionExpired(context.Background(), "sig", 500)
			require.NoError(t, err)
			assert.Equal(t, tc.expired, expired)
			if tc.name != "still valid" {
				assert.Equal(t, 1, fallback.count("getSignatureStatuses"), "every endpoint is asked before a transfer is called expired")
			}
		})
	}

	down := newCountingSolanaNode(t, map[string]string{"getBlockHeight": `"result":501`, "getSignatureStatuses": `"error":{"code":-32603,"message":"internal error"}`})
	_, err := devnetWithFallbacks(down.server.URL).TransactionExpired(context.Background(), "sig", 500)
	assert.Error(t, err, "a status that cannot be read proves nothing")
}
