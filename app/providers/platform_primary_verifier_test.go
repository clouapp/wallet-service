package providers

import (
	"context"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

// The platform API (PATCH /v1/platform/chains/{chainId}/rpc) never moves a chain to
// another network, whatever CHAIN_NETWORK_PROFILE says: only chains:set-rpc then
// chains:align-network do, from artisan.
func TestPlatform_PrimaryVerifier_NeverFollowsTheNetworkProfile(t *testing.T) {
	const profileKey = "vault.chains.network_profile"
	previous := facades.Config().GetString(profileKey)
	facades.Config().Add(profileKey, models.ChainNetworkProfileTestnet)
	t.Cleanup(func() { facades.Config().Add(profileKey, previous) })
	mainnetID := models.EVMNetworkIDEthereumMainnet
	eth := models.Chain{ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: &mainnetID}
	sepolia := newChainIDServer(t, "0xaa36a7")

	_, moved, err := platformPrimaryVerifier().Verify(context.Background(), eth, sepolia)
	require.ErrorIs(t, err, chainpkg.ErrWrongNetwork, "the testnet profile would move eth to Sepolia; the platform API must refuse it")
	assert.False(t, moved)

	mainnet := newChainIDServer(t, "0x1")
	network, moved, err := platformPrimaryVerifier().Verify(context.Background(), eth, mainnet)
	require.NoError(t, err)
	assert.False(t, moved)
	assert.Equal(t, models.NetworkEthereumMainnet, network)
}

func newChainIDServer(t *testing.T, chainIDHex string) string {
	t.Helper()
	server, _ := chainIDNode(t, chainIDHex)
	return server.URL
}
