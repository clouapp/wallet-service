package kyc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/bridge"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/ozav"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Every (rail, token, chain) the read model computes over has to be real on
// some adapter, or it promises a capability nothing can grant. The names come
// from models (RampTokens, ChainSolana); each adapter keeps its own subset.
func TestCapabilityVocabularyIsSupportedBySomeRealAdapter(t *testing.T) {
	noSecret := func(context.Context) (string, error) { return "", nil }
	adapters := []ramp.ProviderAdapter{
		bridge.New(
			func(context.Context) (*bridge.Client, error) {
				return nil, errors.New("vocabulary test never calls the client")
			},
			noSecret,
		),
		ozav.New(
			func(context.Context) (*ozav.Client, error) {
				return nil, errors.New("vocabulary test never calls the client")
			},
			noSecret,
		),
	}

	for _, rail := range ramp.PixRails() {
		for _, token := range models.RampTokens {
			cap := ramp.Capability{Rail: rail, Token: token, Chain: models.ChainSolana}
			supported := false
			for _, adapter := range adapters {
				if adapter.Supports(cap) {
					supported = true
					break
				}
			}
			require.True(t, supported,
				"%s/%s/%s is in the read model's vocabulary but no real adapter supports it",
				rail, token, models.ChainSolana)
		}
	}
}
