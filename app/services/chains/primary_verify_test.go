package chains_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// fixedVerifier answers every endpoint with the same network check.
type fixedVerifier struct {
	network string
	moved   bool
	err     error
	asked   []string
}

func (v *fixedVerifier) Verify(_ context.Context, _ models.Chain, candidate string) (string, bool, error) {
	v.asked = append(v.asked, candidate)
	return v.network, v.moved, v.err
}

var servesItsNetwork = &fixedVerifier{network: models.NetworkEthereumSepolia}

func TestUpdate_RPC_RefusesAnEndpointOnAnotherNetworkOrThatDoesNotAnswer(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want string
	}{
		"wrong network": {fmt.Errorf("%w: eth_chainId is 1", chain.ErrWrongNetwork), "refused, nothing written: rpc endpoint serves another network"},
		"no answer":     {fmt.Errorf("%w: eth_chainId: provider unavailable", chainregistry.ErrEndpointUnverified), "refused, nothing written: rpc endpoint did not say which network it serves"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &rpcStore{chain: ethChain("1", "1", "1"), sealed: "kept"}
			store.chain.RpcURL = "kept"
			activity := &thresholdActivity{}
			verifier := &fixedVerifier{err: tc.err}
			service := chainsvc.NewRPC(chainsvc.RPCDeps{
				Store: store, Admins: &thresholdAdmins{allow: true}, Activity: activity, Sealer: prefixSeal{}, Dialer: &hostDialer{}, Verify: verifier,
			})

			_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"http://5.104.72.66:8545"}`))
			var invalid *chainsvc.ValidationError
			require.ErrorAs(t, err, &invalid)
			require.Len(t, invalid.Fields["rpcUrl"], 1)
			assert.Contains(t, invalid.Fields["rpcUrl"][0], tc.want)
			assert.Equal(t, "kept", store.sealed)
			assert.Empty(t, activity.rows)
			assert.Equal(t, []string{"http://5.104.72.66:8545"}, verifier.asked)
		})
	}
}

func TestUpdate_RPC_WritesAChainWhoseEndpointsCannotBeAskedAsBefore(t *testing.T) {
	t.Parallel()

	store := &rpcStore{chain: ethChain("1", "1", "1")}
	service := chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: store, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{},
		Verify: &fixedVerifier{err: chainregistry.ErrEndpointIdentityUnsupported},
	})

	view, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"https://dial.example/v2/key"}`))
	require.NoError(t, err)
	assert.True(t, view.RPCURLSet)
	assert.Equal(t, "sealed:https://dial.example/v2/key", store.sealed)
}

func TestNew_RPC_RequiresTheVerifier(t *testing.T) {
	t.Parallel()

	defer func() {
		assert.Equal(t, "chain rpc: endpoint verifier is required", recover())
	}()
	chainsvc.NewRPC(chainsvc.RPCDeps{
		Store: &rpcStore{}, Admins: &thresholdAdmins{}, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{},
	})
	t.Fatal(errors.New("expected a panic"))
}
