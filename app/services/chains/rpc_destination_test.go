package chains_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/tests/fakedns"
)

func TestUpdate_RPC_RefusesALinkLocalOrMetadataEndpointBeforeAskingIt(t *testing.T) {
	t.Setenv("CHAIN_RPC_TEST_METADATA_URL", "http://169.254.169.254/latest/meta-data/")
	cases := map[string]string{
		"http://169.254.169.254/latest/meta-data/": egress.ReasonLinkLocal,
		"http://169.254.170.2/v2/credentials":      egress.ReasonLinkLocal,
		"http://[fe80::1]:8545":                    egress.ReasonLinkLocal,
		"http://metadata.google.internal/":         egress.ReasonMetadata,
		"http://0.0.0.0:8545":                      egress.ReasonReserved,
		"env:CHAIN_RPC_TEST_METADATA_URL":          egress.ReasonLinkLocal,
		"http://169.254.169.254.nip.io/latest/":    egress.ReasonResolved,
		"http://10.0.0.5:25":                       egress.ReasonPort,
	}
	for url, reason := range cases {
		store := &rpcStore{chain: ethChain("1", "1", "1"), sealed: "kept"}
		store.chain.RpcURL = "kept"
		verifier := &fixedVerifier{network: models.NetworkEthereumSepolia}
		service := chainsvc.NewRPC(chainsvc.RPCDeps{
			Store: store, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{}, Verify: verifier,
			Destinations: testRPCDestinations(t),
		})

		_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"`+url+`"}`))
		var invalid *chainsvc.ValidationError
		require.ErrorAs(t, err, &invalid, url)
		assert.Equal(t, []string{reason}, invalid.Fields["rpcUrl"], url)
		assert.Empty(t, verifier.asked, "%s was asked for its network", url)
		assert.Equal(t, "kept", store.sealed, url)
	}
}

func TestUpdate_RPC_AcceptsAPrivateNodeAndALinkLocalOneWithTheFlag(t *testing.T) {
	for _, tc := range []struct {
		url            string
		allowLinkLocal bool
	}{
		{"http://10.0.0.5:8545", false},
		{"http://127.0.0.1:8545", false},
		{"http://169.254.10.20:8545", true},
	} {
		store := &rpcStore{chain: ethChain("1", "1", "1"), sealed: "kept"}
		store.chain.RpcURL = "kept"
		service := chainsvc.NewRPC(chainsvc.RPCDeps{
			Store: store, Admins: &thresholdAdmins{allow: true}, Activity: &thresholdActivity{}, Sealer: prefixSeal{}, Dialer: &hostDialer{},
			Verify: &fixedVerifier{network: models.NetworkEthereumSepolia}, Destinations: egress.NewGuard(egress.RPCPolicy(tc.allowLinkLocal), nil),
		})
		_, err := service.Update(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"rpcUrl":"`+tc.url+`"}`))
		require.NoError(t, err, tc.url)
	}
}

// testRPCDestinations is the RPC guard over a fake DNS server: 169.254.169.254.nip.io
// resolves to the metadata address, any other name does not resolve, so no test asks
// the network.
func testRPCDestinations(t *testing.T) *egress.Guard {
	t.Helper()
	return egress.NewGuard(egress.RPCPolicy(false), fakedns.Start(t, map[string][]string{
		"169.254.169.254.nip.io": {"169.254.169.254"},
	}).Resolver())
}
