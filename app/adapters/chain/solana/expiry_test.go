package solana

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	_ chain.ExpiryEvidenceReader = (*SolanaLive)(nil)
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

func TestSolana_ExpiryEvidence_CountsOnlyFreshAnswersThatDoNotKnowIt(t *testing.T) {
	const epoch = `"result":{"absoluteSlot":900,"blockHeight":501,"epoch":1,"slotIndex":1,"slotsInEpoch":432000}`
	status := func(slot int, known bool) string {
		value := `[null]`
		if known {
			value = `[{"slot":450,"confirmations":null,"err":null,"confirmationStatus":"finalized"}]`
		}
		return fmt.Sprintf(`"result":{"context":{"slot":%d},"value":%s}`, slot, value)
	}
	for _, tc := range []struct {
		name            string
		height          string
		primary, others string
		want            chain.ExpiryEvidence
	}{
		{"still valid", `"result":{"absoluteSlot":900,"blockHeight":500}`, status(900, false), status(900, false), chain.ExpiryEvidence{}},
		{"two fresh endpoints do not know it", epoch, status(900, false), status(901, false), chain.ExpiryEvidence{Past: true, Witnesses: 2}},
		{"a backend behind the finalized slot is no witness", epoch, status(900, false), status(899, false), chain.ExpiryEvidence{Past: true, Witnesses: 1}},
		{"another endpoint has it", epoch, status(900, false), status(905, true), chain.ExpiryEvidence{Past: true, Known: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := newCountingSolanaNode(t, map[string]string{"getEpochInfo": tc.height, "getSignatureStatuses": tc.primary})
			fallback := newCountingSolanaNode(t, map[string]string{"getGenesisHash": devnetGenesisResult, "getEpochInfo": tc.height, "getSignatureStatuses": tc.others})
			adapter := devnetWithFallbacks(primary.server.URL, fallback.server.URL)

			got, err := adapter.ExpiryEvidence(context.Background(), "sig", 500)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	alone := newCountingSolanaNode(t, map[string]string{"getEpochInfo": epoch, "getSignatureStatuses": status(900, false)})
	got, err := devnetWithFallbacks(alone.server.URL).ExpiryEvidence(context.Background(), "sig", 500)
	require.NoError(t, err)
	assert.Equal(t, chain.ExpiryEvidence{Past: true, Witnesses: 1}, got, "one endpoint is one witness: the tracker waits for a second look")

	down := newCountingSolanaNode(t, map[string]string{"getEpochInfo": epoch, "getSignatureStatuses": `"error":{"code":-32603,"message":"internal error"}`})
	_, err = devnetWithFallbacks(down.server.URL).ExpiryEvidence(context.Background(), "sig", 500)
	assert.Error(t, err, "a status that cannot be read proves nothing")
}
