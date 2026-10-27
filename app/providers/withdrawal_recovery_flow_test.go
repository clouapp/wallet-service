package providers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/app/services/withdrawrecoveryflow"
	runtime "github.com/macrowallets/waas/app/services/withdrawrecoveryruntime"
)

type recoveryRPCStub struct{ calls int }

func (r *recoveryRPCStub) Call(context.Context, string, interface{}, ...interface{}) error {
	r.calls++
	return nil
}
func TestRecoveryFlowResolutionIsLazyAndScopeFailsClosed(t *testing.T) {
	(&WithdrawalServiceProvider{}).Register(foundation.App)
	flow, err := container.Make[*withdrawrecoveryflow.Flow]()
	require.NoError(t, err)
	require.NotNil(t, flow)
	require.Same(t, flow, container.MustMake[*withdrawrecoveryflow.Flow]())
	// No executable REVISION/config/database/RPC is needed for startup, and no
	// unauthenticated request may reach those dependencies.
	_, err = flow.Lookup(context.Background(), [16]byte{1}, [16]byte{2})
	require.ErrorIs(t, err, recovery.ErrNotFound)
}
func TestRecoveryRevisionRequiresActualFullCommit(t *testing.T) {
	for _, value := range []string{"", "dev", "unknown", strings.Repeat("a", 39), strings.Repeat("A", 40), strings.Repeat("a", 41), "refs/heads/main"} {
		_, err := recoveryRevision([]byte(value))
		require.Error(t, err)
	}
	value, err := recoveryRevision([]byte(strings.Repeat("a", 40) + "\n"))
	require.NoError(t, err)
	require.Len(t, value, 40)
}
func TestRecoveryRPCPairUsesOnlyDistinctConfiguredHTTPSProviders(t *testing.T) {
	for _, tc := range []struct {
		name, primary, secondary string
		enabled                  bool
		chain                    string
		valid                    bool
	}{
		{"independent", "https://a.test/private-token?key=secret", "https://b.test/private-token?key=other", true, "arbitrum", true},
		{"same provider", "https://a.test/one", "https://a.test/two", true, "arbitrum", false},
		{"http", "http://a.test", "https://b.test", true, "arbitrum", false},
		{"disabled", "https://a.test", "https://b.test", false, "arbitrum", false},
		{"foreign chain", "https://a.test", "https://b.test", true, "eth", false},
		{"credentials", "https://user:secret@a.test", "https://b.test", true, "arbitrum", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dialed []string
			var clients []*recoveryRPCStub
			pair, err := recoveryRPCPair(&models.Chain{ID: "arbitrum", RpcURL: tc.primary}, []models.ChainRPCEndpoint{{ChainID: tc.chain, URL: tc.secondary, Enabled: tc.enabled}}, func(v string) (string, error) { return v, nil }, func(v string) runtime.RPC {
				dialed = append(dialed, v)
				c := &recoveryRPCStub{}
				clients = append(clients, c)
				return c
			}, func(context.Context) error { return nil })
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, []string{tc.primary, tc.secondary}, dialed)
				require.Equal(t, "https://a.test", pair[0].Origin)
				require.Equal(t, "https://b.test", pair[1].Origin)
				require.NotSame(t, pair[0].Client, pair[1].Client)
				require.Zero(t, clients[0].calls+clients[1].calls)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
			}
		})
	}
}
func TestRecoveryRPCWriteGateIsRefreshedAtTransmission(t *testing.T) {
	client := &recoveryRPCStub{}
	paused := false
	gateCalls := 0
	rpc := &recoveryGatedRPC{RPC: client, guard: func(context.Context) error {
		gateCalls++
		if paused {
			return errors.New("paused")
		}
		return nil
	}}
	require.NoError(t, rpc.Call(context.Background(), "eth_chainId", nil))
	require.Zero(t, gateCalls)
	paused = true
	require.Error(t, rpc.Call(context.Background(), "eth_sendRawTransaction", nil, "0xsynthetic"))
	require.Equal(t, 1, client.calls)
	paused = false
	require.NoError(t, rpc.Call(context.Background(), "eth_sendRawTransaction", nil, "0xsynthetic"))
	require.Equal(t, 2, client.calls)
	require.Equal(t, 2, gateCalls)
}
