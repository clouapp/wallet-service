package chainregistry

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

func TestPrimary_Verifier_AcceptsTheChainsNetworkOrTheOneTheProfileMovesItTo(t *testing.T) {
	ctx := context.Background()
	mainnetID := models.EVMNetworkIDEthereumMainnet
	eth := models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: &mainnetID, RpcURL: "stored"}
	mainnet := answeringNode(t, map[string]string{"eth_chainId": `"0x1"`})
	sepolia := answeringNode(t, map[string]string{"eth_chainId": `"0xaa36a7"`})
	amoy := answeringNode(t, map[string]string{"eth_chainId": `"0x13882"`})

	own := NewPrimaryVerifier(nil, func() string { return models.ChainNetworkProfileMainnet })
	network, moved, err := own.Verify(ctx, eth, mainnet)
	require.NoError(t, err)
	assert.False(t, moved)
	assert.Equal(t, models.NetworkEthereumMainnet, network)
	_, _, err = own.Verify(ctx, eth, sepolia)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork, "the mainnet profile does not move eth to Sepolia")

	moving := NewPrimaryVerifier(nil, func() string { return models.ChainNetworkProfileTestnet })
	network, moved, err = moving.Verify(ctx, eth, sepolia)
	require.NoError(t, err)
	assert.True(t, moved, "the testnet profile moves eth to Sepolia next")
	assert.Equal(t, models.NetworkEthereumSepolia, network)
	_, _, err = moving.Verify(ctx, eth, amoy)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork)

	teth := eth
	teth.ID = models.ChainTETH
	_, _, err = moving.Verify(ctx, teth, sepolia)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork, "no profile decides a t-prefixed record")
}

func TestPrimary_Verifier_ReadsTheSolanaClusterFromTheCurrentPrimary(t *testing.T) {
	ctx := context.Background()
	sol := models.Chain{ID: models.ChainTSOL, AdapterType: models.AdapterTypeSolana, IsTestnet: true, RpcURL: "stored"}
	testnetNode := answeringNode(t, map[string]string{"getGenesisHash": `"` + models.SolanaGenesisHash(models.NetworkSolanaTestnet) + `"`})
	opened := func(stored string) (string, error) {
		if stored != "stored" {
			return "", errors.New("open")
		}
		return "https://api.testnet.solana.com", nil
	}

	network, moved, err := NewPrimaryVerifier(opened, nil).Verify(ctx, sol, testnetNode)
	require.NoError(t, err)
	assert.False(t, moved)
	assert.Equal(t, models.NetworkSolanaTestnet, network)

	_, _, err = NewPrimaryVerifier(nil, nil).Verify(ctx, sol, testnetNode)
	assert.ErrorIs(t, err, chain.ErrWrongNetwork, "without its primary the record is devnet by is_testnet")
}

func TestPrimary_Verifier_LeavesAChainWithoutAnIdentityCallUnchecked(t *testing.T) {
	btc := models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin}
	_, _, err := NewPrimaryVerifier(nil, nil).Verify(context.Background(), btc, "https://mempool.space/testnet4/api")
	assert.ErrorIs(t, err, ErrEndpointIdentityUnsupported)

	_, _, err = NewPrimaryVerifier(nil, nil).Verify(context.Background(), btc, "env:PRIMARY_VERIFY_TEST_UNSET")
	assert.ErrorIs(t, err, ErrEndpointIdentityUnsupported, "nothing is resolved for a chain that is not checked")

	eth := models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM}
	_, _, err = NewPrimaryVerifier(nil, nil).Verify(context.Background(), eth, "env:PRIMARY_VERIFY_TEST_UNSET")
	assert.ErrorIs(t, err, ErrEndpointUnverified, "an env reference that does not resolve here cannot be checked")
}
