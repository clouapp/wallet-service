package chains_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	activitylog "github.com/macrowallets/waas/app/services/activity"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/rpcendpoints"
)

// fakeEndpointList answers Show for the chains it knows and records the change
// it was asked to apply; a change runs record like the real service does.
type fakeEndpointList struct {
	known   map[string]bool
	changes []rpcendpoints.Change
	refuse  error
	changed bool
}

func (f *fakeEndpointList) Show(_ context.Context, chainID string, probe bool) (rpcendpoints.View, error) {
	if !f.known[chainID] {
		return rpcendpoints.View{}, rpcendpoints.ErrChainNotFound
	}
	view := rpcendpoints.View{ChainID: chainID, Endpoints: []rpcendpoints.Endpoint{{Position: 1, Primary: true, Host: "sepolia.drpc.org", Enabled: true, Usable: true}}}
	if probe {
		view.Endpoints[0].Fingerprint = "probed"
	}
	return view, nil
}

func (f *fakeEndpointList) ApplyRecorded(ctx context.Context, chainID string, change rpcendpoints.Change, record func(context.Context, rpcendpoints.Plan) error) (rpcendpoints.Plan, error) {
	f.changes = append(f.changes, change)
	if f.refuse != nil {
		return rpcendpoints.Plan{}, f.refuse
	}
	plan := rpcendpoints.Plan{Change: change, Before: rpcendpoints.View{ChainID: chainID}, Changed: f.changed}
	if !plan.Changed {
		return plan, nil
	}
	if err := record(ctx, plan); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

func endpointEditor(list *fakeEndpointList, admin bool) (*chainsvc.RPCEndpoints, *thresholdActivity) {
	activity := &thresholdActivity{}
	return chainsvc.NewRPCEndpoints(chainsvc.RPCEndpointsDeps{List: list, Admins: &thresholdAdmins{allow: admin}, Activity: activity}), activity
}

func TestRPC_Endpoints_UnknownChainIsNotFoundBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	editor, _ := endpointEditor(&fakeEndpointList{}, false)
	_, err := editor.List(context.Background(), uuid.New(), "missing", false)
	assert.ErrorIs(t, err, chainsvc.ErrNotFound)
	_, err = editor.Add(context.Background(), uuid.New(), "missing", thresholdObject(t, `{"url":"https://sepolia.drpc.org"}`))
	assert.ErrorIs(t, err, chainsvc.ErrNotFound)
}

func TestRPC_Endpoints_ANonAdminChangesNothing(t *testing.T) {
	t.Parallel()

	list := &fakeEndpointList{known: map[string]bool{"eth": true}, changed: true}
	editor, activity := endpointEditor(list, false)
	_, err := editor.List(context.Background(), uuid.New(), "eth", false)
	assert.ErrorIs(t, err, chainsvc.ErrPlatformForbidden)
	_, err = editor.Remove(context.Background(), uuid.New(), "eth", 2)
	assert.ErrorIs(t, err, chainsvc.ErrPlatformForbidden)
	assert.Empty(t, list.changes)
	assert.Empty(t, activity.rows)
}

func TestRPC_Endpoints_ListAsksEveryEndpointOnlyWithProbe(t *testing.T) {
	t.Parallel()

	editor, _ := endpointEditor(&fakeEndpointList{known: map[string]bool{"eth": true}}, true)
	plain, err := editor.List(context.Background(), uuid.New(), "eth", false)
	require.NoError(t, err)
	probed, err := editor.List(context.Background(), uuid.New(), "eth", true)
	require.NoError(t, err)
	assert.Empty(t, plain.Endpoints[0].Fingerprint)
	assert.Equal(t, "probed", probed.Endpoints[0].Fingerprint)
}

func TestRPC_Endpoints_BodiesBecomeOneChangeEach(t *testing.T) {
	t.Parallel()

	list := &fakeEndpointList{known: map[string]bool{"eth": true}, changed: true}
	editor, activity := endpointEditor(list, true)
	ctx := context.Background()
	actor := uuid.New()

	_, err := editor.Add(ctx, actor, "eth", thresholdObject(t, `{"url":" https://sepolia.drpc.org ","position":2}`))
	require.NoError(t, err)
	_, err = editor.Replace(ctx, actor, "eth", thresholdObject(t, `{"urls":["https://a.example","https://b.example"]}`))
	require.NoError(t, err)
	_, err = editor.Reorder(ctx, actor, "eth", thresholdObject(t, `{"order":[2,1]}`))
	require.NoError(t, err)
	_, err = editor.SetEnabled(ctx, actor, "eth", 3, thresholdObject(t, `{"enabled":false}`))
	require.NoError(t, err)
	_, err = editor.SetEnabled(ctx, actor, "eth", 3, thresholdObject(t, `{"enabled":true}`))
	require.NoError(t, err)
	_, err = editor.Remove(ctx, actor, "eth", 3)
	require.NoError(t, err)
	_, err = editor.AddDefaults(ctx, actor, "eth")
	require.NoError(t, err)

	assert.Equal(t, []rpcendpoints.Change{
		{Action: rpcendpoints.ActionAdd, URL: "https://sepolia.drpc.org", Position: 2},
		{Action: rpcendpoints.ActionSet, URLs: []string{"https://a.example", "https://b.example"}},
		{Action: rpcendpoints.ActionOrder, Order: []int{2, 1}},
		{Action: rpcendpoints.ActionDisable, Position: 3},
		{Action: rpcendpoints.ActionEnable, Position: 3},
		{Action: rpcendpoints.ActionRemove, Position: 3},
		{Action: rpcendpoints.ActionDefaults},
	}, list.changes)

	require.Len(t, activity.rows, 7)
	row := activity.rows[0]
	assert.Equal(t, actor, row.ActorUserID)
	assert.Nil(t, row.AccountID)
	assert.Equal(t, activitylog.ActionChainsUpdated, row.Action)
	meta, err := row.Metadata.Encode()
	require.NoError(t, err)
	assert.Equal(t, `{"fields":["rpc_endpoints"],"key":"eth"}`, meta)
}

func TestRPC_Endpoints_RefusesABadBodyBeforeAnyChange(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		call  func(*chainsvc.RPCEndpoints) error
		field string
	}{
		"missing url": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Add(context.Background(), uuid.New(), "eth", thresholdObject(t, `{}`))
			return err
		}, "url"},
		"unknown field": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Add(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"url":"https://a.example","rpcUrl":"x"}`))
			return err
		}, "rpcUrl"},
		"position type": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Add(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"url":"https://a.example","position":"2"}`))
			return err
		}, "position"},
		"position zero": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Add(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"url":"https://a.example","position":0}`))
			return err
		}, "position"},
		"empty list": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Replace(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"urls":[]}`))
			return err
		}, "urls"},
		"order type": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.Reorder(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"order":"2,1"}`))
			return err
		}, "order"},
		"enabled null": {func(e *chainsvc.RPCEndpoints) error {
			_, err := e.SetEnabled(context.Background(), uuid.New(), "eth", 2, thresholdObject(t, `{"enabled":null}`))
			return err
		}, "enabled"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			list := &fakeEndpointList{known: map[string]bool{"eth": true}, changed: true}
			editor, activity := endpointEditor(list, true)
			err := tc.call(editor)
			var invalid *chainsvc.ValidationError
			require.ErrorAs(t, err, &invalid)
			assert.NotEmpty(t, invalid.Fields[tc.field], "fields = %v", invalid.Fields)
			assert.Empty(t, list.changes)
			assert.Empty(t, activity.rows)
		})
	}
}

func TestRPC_Endpoints_ARefusedChangeIsAValidationErrorAndRecordsNothing(t *testing.T) {
	t.Parallel()

	refusal := &rpcendpoints.Refusal{Field: "url", Reason: "refused, nothing written: position 2 ethereum-rpc.publicnode.com serves another network"}
	list := &fakeEndpointList{known: map[string]bool{"eth": true}, refuse: refusal}
	editor, activity := endpointEditor(list, true)

	_, err := editor.Add(context.Background(), uuid.New(), "eth", thresholdObject(t, `{"url":"https://ethereum-rpc.publicnode.com"}`))
	var invalid *chainsvc.ValidationError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{refusal.Reason}, invalid.Fields["url"])
	assert.Empty(t, activity.rows)

	list.refuse = errors.New("database down")
	_, err = editor.Remove(context.Background(), uuid.New(), "eth", 2)
	assert.EqualError(t, err, "database down")
}

func TestRPC_Endpoints_AChangeThatChangesNothingRecordsNothing(t *testing.T) {
	t.Parallel()

	list := &fakeEndpointList{known: map[string]bool{"eth": true}}
	editor, activity := endpointEditor(list, true)
	plan, err := editor.SetEnabled(context.Background(), uuid.New(), "eth", 2, thresholdObject(t, `{"enabled":true}`))
	require.NoError(t, err)
	assert.False(t, plan.Changed)
	assert.Empty(t, activity.rows)
}

func TestNew_RPCEndpoints_RequiresEveryDependency(t *testing.T) {
	t.Parallel()

	for name, deps := range map[string]chainsvc.RPCEndpointsDeps{
		"list":     {Admins: &thresholdAdmins{}, Activity: &thresholdActivity{}},
		"admins":   {List: &fakeEndpointList{}, Activity: &thresholdActivity{}},
		"activity": {List: &fakeEndpointList{}, Admins: &thresholdAdmins{}},
	} {
		assert.Panics(t, func() { chainsvc.NewRPCEndpoints(deps) }, name)
	}
}
