package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/contracts/console/command"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

type fakeEndpointRunner struct {
	req    rpcendpoints.Request
	calls  int
	report rpcendpoints.Report
	err    error
}

func (f *fakeEndpointRunner) Run(_ context.Context, req rpcendpoints.Request) (rpcendpoints.Report, error) {
	f.calls++
	f.req = req
	return f.report, f.err
}

// endpointOptions are the options of one chains:rpc-endpoints invocation; a flag
// left out answers its zero value.
type endpointOptions map[string]string

func rpcEndpointsContext(t *testing.T, chainID string, options endpointOptions, bools ...string) *mocksconsole.Context {
	t.Helper()
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain_id").Return(chainID).Maybe()
	for _, name := range []string{"add", "position", "remove", "enable", "disable", "order", "set"} {
		ctx.EXPECT().Option(name).Return(options[name]).Maybe()
	}
	set := map[string]bool{}
	for _, name := range bools {
		set[name] = true
	}
	for _, name := range []string{"defaults", "probe", "apply"} {
		ctx.EXPECT().OptionBool(name).Return(set[name]).Maybe()
	}
	return ctx
}

func TestChains_RPCEndpoints_OffersEveryChangeAndApply(t *testing.T) {
	cmd := NewChainsRPCEndpoints(&fakeEndpointRunner{})
	assert.Equal(t, "chains:rpc-endpoints", cmd.Signature())
	assert.NotEmpty(t, cmd.Description())
	ext := cmd.Extend()
	assert.Equal(t, "chains", ext.Category)
	require.Len(t, ext.Arguments, 1)
	flags := map[string]bool{}
	for _, flag := range ext.Flags {
		switch typed := flag.(type) {
		case *command.StringFlag:
			flags[typed.Name] = true
		case *command.BoolFlag:
			flags[typed.Name] = true
		}
	}
	for _, name := range []string{"add", "position", "remove", "enable", "disable", "order", "set", "defaults", "probe", "apply"} {
		assert.True(t, flags[name], "missing --%s", name)
	}
}

func TestChains_RPCEndpoints_ListsWithoutAChange(t *testing.T) {
	runner := &fakeEndpointRunner{report: rpcendpoints.Report{Info: []string{"chain eth (evm, ethereum-sepolia): 1 endpoint(s)"}}}
	ctx := rpcEndpointsContext(t, " eth ", nil, "probe")
	ctx.EXPECT().Info(runner.report.Info[0]).Return()

	require.NoError(t, NewChainsRPCEndpoints(runner).Handle(ctx))
	assert.Equal(t, rpcendpoints.Request{ChainID: "eth", Probe: true}, runner.req)
}

func TestChains_RPCEndpoints_PassesOneChangeToTheService(t *testing.T) {
	cases := map[string]struct {
		options endpointOptions
		bools   []string
		want    rpcendpoints.Change
	}{
		"add":      {endpointOptions{"add": "https://sepolia.drpc.org", "position": "2"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionAdd, URL: "https://sepolia.drpc.org", Position: 2}},
		"remove":   {endpointOptions{"remove": "3"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionRemove, Position: 3}},
		"enable":   {endpointOptions{"enable": "2"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionEnable, Position: 2}},
		"disable":  {endpointOptions{"disable": "2"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionDisable, Position: 2}},
		"order":    {endpointOptions{"order": "2, 1,3"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionOrder, Order: []int{2, 1, 3}}},
		"set":      {endpointOptions{"set": "https://a.example,env:ETH_RPC_URL"}, nil, rpcendpoints.Change{Action: rpcendpoints.ActionSet, URLs: []string{"https://a.example", "env:ETH_RPC_URL"}}},
		"defaults": {nil, []string{"defaults"}, rpcendpoints.Change{Action: rpcendpoints.ActionDefaults}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeEndpointRunner{}
			ctx := rpcEndpointsContext(t, "eth", tc.options, append(tc.bools, "apply")...)

			require.NoError(t, NewChainsRPCEndpoints(runner).Handle(ctx))
			require.NotNil(t, runner.req.Change)
			assert.Equal(t, tc.want, *runner.req.Change)
			assert.True(t, runner.req.Apply)
		})
	}
}

func TestChains_RPCEndpoints_RefusesTwoChangesOrABadPositionBeforeTheService(t *testing.T) {
	cases := map[string]endpointOptions{
		"two changes":       {"remove": "2", "disable": "3"},
		"position alone":    {"position": "2"},
		"not a position":    {"remove": "first"},
		"zero position":     {"enable": "0"},
		"bad order element": {"order": "2,x"},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeEndpointRunner{}
			ctx := rpcEndpointsContext(t, "eth", options)
			ctx.EXPECT().Error(mock.AnythingOfType("string")).Return().Once()

			assert.Error(t, NewChainsRPCEndpoints(runner).Handle(ctx))
			assert.Zero(t, runner.calls)
		})
	}
}

func TestChains_RPCEndpoints_PrintsTheReportAndFailsOnARefusal(t *testing.T) {
	refusal := &rpcendpoints.Refusal{Field: "url", Reason: "refused, nothing written: position 2 ethereum-rpc.publicnode.com serves another network"}
	runner := &fakeEndpointRunner{
		report: rpcendpoints.Report{Info: []string{"chain eth (evm, ethereum-sepolia): 1 endpoint(s)"}, Warning: []string{"position 2 ethereum-rpc.publicnode.com: wrong network"}},
		err:    refusal,
	}
	ctx := rpcEndpointsContext(t, "eth", endpointOptions{"add": "https://ethereum-rpc.publicnode.com"}, "apply")
	ctx.EXPECT().Warning(runner.report.Warning[0]).Return()
	ctx.EXPECT().Info(runner.report.Info[0]).Return()
	ctx.EXPECT().Error(refusal.Reason).Return()

	err := NewChainsRPCEndpoints(runner).Handle(ctx)
	var got *rpcendpoints.Refusal
	assert.True(t, errors.As(err, &got))
}
