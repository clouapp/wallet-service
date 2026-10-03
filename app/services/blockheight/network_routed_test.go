package blockheight

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

type recordingProvider struct{ asked []string }

func (p *recordingProvider) GetBlockHeight(_ context.Context, chainID string) (uint64, error) {
	p.asked = append(p.asked, chainID)
	return 42, nil
}

func TestRoutedProviderReadsTheTipOfTheNetworkTheRecordPointsAt(t *testing.T) {
	inner := &recordingProvider{}
	routed := RouteByNetwork(inner, map[string]string{
		models.ChainETH:     models.NetworkEthereumSepolia,
		models.ChainPolygon: models.NetworkPolygonAmoy,
		models.ChainBTC:     models.NetworkBitcoinTestnet,
		models.ChainSOL:     models.NetworkSolanaDevnet,
		models.ChainTETH:    models.NetworkEthereumSepolia,
	})
	ctx := context.Background()

	for _, chainID := range []string{models.ChainETH, models.ChainPolygon, models.ChainBTC, models.ChainSOL, models.ChainTETH} {
		height, err := routed.GetBlockHeight(ctx, chainID)
		require.NoError(t, err)
		assert.Equal(t, uint64(42), height)
	}

	assert.Equal(t, []string{models.ChainTETH, models.ChainTPolygon, models.ChainTBTC, models.ChainTSOL, models.ChainTETH}, inner.asked)
}

func TestRoutedProviderKeepsTheChainIDWhenTheNetworkIsUnknown(t *testing.T) {
	inner := &recordingProvider{}
	routed := RouteByNetwork(inner, map[string]string{models.ChainETH: ""})

	_, err := routed.GetBlockHeight(context.Background(), models.ChainETH)
	require.NoError(t, err)
	_, err = routed.GetBlockHeight(context.Background(), models.ChainTBTC)
	require.NoError(t, err)

	assert.Equal(t, []string{models.ChainETH, models.ChainTBTC}, inner.asked)
}

func TestRoutedProviderFailsForANetworkWithoutATipSource(t *testing.T) {
	inner := &recordingProvider{}
	routed := RouteByNetwork(inner, map[string]string{models.ChainSOL: models.NetworkSolanaTestnet})

	_, err := routed.GetBlockHeight(context.Background(), models.ChainSOL)

	assert.Error(t, err)
	assert.Empty(t, inner.asked)
}

func TestRoutedProviderCopiesItsMapAndRejectsANilInner(t *testing.T) {
	networks := map[string]string{models.ChainBTC: models.NetworkBitcoinTestnet}
	inner := &recordingProvider{}
	routed := RouteByNetwork(inner, networks)
	networks[models.ChainBTC] = models.NetworkBitcoinMainnet

	_, err := routed.GetBlockHeight(context.Background(), models.ChainBTC)
	require.NoError(t, err)
	assert.Equal(t, []string{models.ChainTBTC}, inner.asked)

	_, err = RouteByNetwork(nil, nil).GetBlockHeight(context.Background(), models.ChainBTC)
	assert.Error(t, err)
}

func TestRoutedProviderSendsBaseArbitrumAndBSCToTheChainRPC(t *testing.T) {
	inner := &recordingProvider{}
	routed := RouteByNetwork(inner, map[string]string{
		models.ChainBase:      models.NetworkBaseSepolia,
		models.ChainTArbitrum: models.NetworkArbitrumSepolia,
		models.ChainBSC:       models.NetworkBSCMainnet,
	})

	for _, chainID := range []string{models.ChainBase, models.ChainTArbitrum, models.ChainBSC} {
		_, err := routed.GetBlockHeight(context.Background(), chainID)
		assert.ErrorIs(t, err, ErrTipFromChainRPC, chainID)
	}
	assert.Empty(t, inner.asked, "Etherscan must not be asked for networks its free tier does not serve")
}
