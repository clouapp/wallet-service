package swapproviders

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiFiCost_ConvertsRawFeesWithoutLosingBaseUnits(t *testing.T) {
	fee, err := normalizeLiFiCost(lifiCost{Amount: "1000001", Token: lifiToken{Symbol: "USDC", Decimals: 6}}, "provider")
	require.NoError(t, err)
	require.Equal(t, "1.000001", fee.Amount)
	fee, err = normalizeLiFiCost(lifiCost{Amount: "1000000000000001", Token: lifiToken{Symbol: "ETH", Decimals: 18}}, "network")
	require.NoError(t, err)
	require.Equal(t, "0.001000000000000001", fee.Amount)
	require.Equal(t, "network", fee.Name)
}
