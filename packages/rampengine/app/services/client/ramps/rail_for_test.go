package ramps

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// A standing account collects on the rail of its currency; a currency no rail
// collects is refused, never assumed onto a default.
func TestRailFor(t *testing.T) {
	rail, err := railFor("brl")
	require.NoError(t, err)
	require.Equal(t, ramp.RailPixPayin, rail)

	rail, err = railFor("usd")
	require.NoError(t, err)
	require.Equal(t, ramp.RailUSDPayin, rail)

	_, err = railFor("eur")
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}
