package rpcendpoints

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

const (
	filteredNode  = "http://5.104.72.66:8545"
	drpc          = "https://sepolia.drpc.org"
	tenderly      = "https://sepolia.gateway.tenderly.co"
	publicnode    = "https://ethereum-sepolia-rpc.publicnode.com"
	keyedEndpoint = "https://eth-sepolia.g.alchemy.com/v2/secret-api-key"
	mainnetNode   = "https://ethereum-rpc.publicnode.com"
)

// fakeSealer seals by prefixing enc:v1: to a base64 copy, so a stored value never
// holds the URL in clear.
type fakeSealer struct{}

func (fakeSealer) Seal(plaintext string) (string, error) {
	return "enc:v1:" + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (fakeSealer) Open(stored string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, "enc:v1:"))
	if err != nil || !strings.HasPrefix(stored, "enc:v1:") {
		return "", errors.New("open")
	}
	return string(raw), nil
}

func sealed(url string) string {
	value, _ := fakeSealer{}.Seal(url)
	return value
}

// memoryStore is the chains row and the fallback rows of one test.
type memoryStore struct {
	chains       map[string]*models.Chain
	fallbacks    map[string][]models.ChainRPCEndpoint
	primaryWrite int
	replaceWrite int
}

func (m *memoryStore) FindByID(_ context.Context, id string) (*models.Chain, error) {
	record, ok := m.chains[id]
	if !ok {
		return nil, models.ErrRepositoryNotFound
	}
	copied := *record
	return &copied, nil
}

func (m *memoryStore) UpdateRPCURL(_ context.Context, id, sealed string) error {
	m.primaryWrite++
	m.chains[id].RpcURL = sealed
	return nil
}

func (m *memoryStore) ListByChain(_ context.Context, chainID string) ([]models.ChainRPCEndpoint, error) {
	return append([]models.ChainRPCEndpoint(nil), m.fallbacks[chainID]...), nil
}

func (m *memoryStore) Replace(_ context.Context, chainID string, rows []models.ChainRPCEndpoint) error {
	m.replaceWrite++
	m.fallbacks[chainID] = append([]models.ChainRPCEndpoint(nil), rows...)
	return nil
}

func (m *memoryStore) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// networks answers what each URL serves; a URL it does not know does not answer.
type networks map[string]int64

func (n networks) verify(calls *[]string) VerifyFunc {
	return func(_ context.Context, adapterType string, target chainregistry.EndpointTarget, rawURL string) (string, error) {
		*calls = append(*calls, rawURL)
		served, ok := n[rawURL]
		if !ok {
			return "", fmt.Errorf("%w: eth_chainId: provider unavailable", chainregistry.ErrEndpointUnverified)
		}
		if served != target.EVMNetworkID {
			return models.EVMNetworkName(served), fmt.Errorf("%w: eth_chainId is %d", chain.ErrWrongNetwork, served)
		}
		return models.EVMNetworkName(served), nil
	}
}

type recordingDialer struct {
	primary   string
	fallbacks []chain.RPCFallback
}

func (d *recordingDialer) ReplaceEndpoints(_, primary string, fallbacks []chain.RPCFallback) (bool, error) {
	d.primary, d.fallbacks = primary, fallbacks
	return true, nil
}

type fixedHealth chain.RPCHealthSnapshot

func (h fixedHealth) RPCHealth(context.Context, string) (chain.RPCHealthSnapshot, bool, error) {
	return chain.RPCHealthSnapshot(h), true, nil
}

type harness struct {
	store    *memoryStore
	verified []string
	dialer   *recordingDialer
	service  *Service
}

func newHarness(t *testing.T, primary string, fallbacks ...models.ChainRPCEndpoint) *harness {
	t.Helper()
	sepolia := models.EVMNetworkIDEthereumSepolia
	h := &harness{
		store: &memoryStore{
			chains: map[string]*models.Chain{
				models.ChainETH: {ID: models.ChainETH, AdapterType: models.AdapterTypeEVM, NetworkID: &sepolia, IsTestnet: true, RpcURL: sealed(primary)},
				models.ChainBTC: {ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, IsTestnet: true, RpcURL: sealed("https://mempool.space/testnet4/api")},
			},
			fallbacks: map[string][]models.ChainRPCEndpoint{models.ChainETH: fallbacks},
		},
		dialer: &recordingDialer{},
	}
	known := networks{
		filteredNode: models.EVMNetworkIDEthereumSepolia, drpc: models.EVMNetworkIDEthereumSepolia,
		tenderly: models.EVMNetworkIDEthereumSepolia, publicnode: models.EVMNetworkIDEthereumSepolia,
		keyedEndpoint: models.EVMNetworkIDEthereumSepolia, mainnetNode: models.EVMNetworkIDEthereumMainnet,
	}
	h.service = NewService(Deps{
		Chains: h.store, Endpoints: h.store, Sealer: fakeSealer{}, Verify: known.verify(&h.verified), Dialer: h.dialer,
		Probe: func(context.Context, string, chainregistry.EndpointTarget, string) chainregistry.EndpointProbe {
			return chainregistry.EndpointProbe{Network: models.NetworkEthereumSepolia, Head: 11937030, Latency: 120 * time.Millisecond}
		},
		Now: func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) },
	})
	return h
}

func fallbackRow(url string, position int, enabled bool) models.ChainRPCEndpoint {
	return models.ChainRPCEndpoint{ChainID: models.ChainETH, URL: sealed(url), Position: position, Enabled: enabled}
}

func (h *harness) storedList(t *testing.T) []string {
	t.Helper()
	primary, err := fakeSealer{}.Open(h.store.chains[models.ChainETH].RpcURL)
	require.NoError(t, err)
	list := []string{primary}
	for _, row := range h.store.fallbacks[models.ChainETH] {
		require.True(t, strings.HasPrefix(row.URL, "enc:v1:"), "a fallback must be stored sealed")
		url, err := fakeSealer{}.Open(row.URL)
		require.NoError(t, err)
		if !row.Enabled {
			url = "disabled:" + url
		}
		list = append(list, fmt.Sprintf("%d:%s", row.Position, url))
	}
	return list
}

func TestShow_Lists_TheEndpointsByHostWithTheirRecordedHealth(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(keyedEndpoint, 2, true), fallbackRow(drpc, 3, false))
	cooling := time.Date(2026, 10, 19, 12, 4, 0, 0, time.UTC)
	h.service.health = fixedHealth{
		ObservedAt: time.Date(2026, 10, 19, 11, 59, 0, 0, time.UTC), Process: "deposit_scanner",
		Endpoints: []chain.RPCEndpointHealth{{
			Position: 1, Host: "5.104.72.66:8545", Fingerprint: chain.RPCEndpointFingerprint(filteredNode),
			ConsecutiveFailures: 3, CoolingDownUntil: cooling, LastError: "timeout", LastErrorAt: cooling.Add(-30 * time.Second),
		}},
	}

	view, err := h.service.Show(context.Background(), models.ChainETH, true)
	require.NoError(t, err)
	require.Len(t, view.Endpoints, 3)
	assert.True(t, view.FailsOver)
	assert.Equal(t, models.NetworkEthereumSepolia, view.Network)
	assert.Equal(t, []string{"5.104.72.66:8545", "eth-sepolia.g.alchemy.com", "sepolia.drpc.org"},
		[]string{view.Endpoints[0].Host, view.Endpoints[1].Host, view.Endpoints[2].Host})
	assert.False(t, view.Endpoints[2].Enabled)
	require.NotNil(t, view.Endpoints[0].Health)
	assert.Equal(t, 3, view.Endpoints[0].Health.ConsecutiveFailures)
	assert.Nil(t, view.Endpoints[1].Health)
	require.NotNil(t, view.Endpoints[1].Probe)
	assert.Equal(t, uint64(11937030), view.Endpoints[1].Probe.Head)

	report, err := h.service.Run(context.Background(), Request{ChainID: models.ChainETH})
	require.NoError(t, err)
	text := strings.Join(append(report.Info, report.Warning...), "\n")
	assert.Contains(t, text, "1 primary  enabled  5.104.72.66:8545  health: skipped until 2026-10-19T12:04:00Z, 3 failure(s) in a row, last error timeout")
	assert.Contains(t, text, "3 fallback disabled sepolia.drpc.org  health: not recorded")
	assert.Contains(t, text, "health recorded by the deposit_scanner process at 2026-10-19T11:59:00Z")
	assert.NotContains(t, text, "secret-api-key")
	assert.NotContains(t, text, "/v2/")
}

func TestShow_Of_AnUnknownChainIsNotFound(t *testing.T) {
	h := newHarness(t, filteredNode)
	_, err := h.service.Show(context.Background(), "nope", false)
	assert.ErrorIs(t, err, ErrChainNotFound)
	_, err = h.service.Apply(context.Background(), " ", Change{Action: ActionDefaults}, true)
	assert.ErrorIs(t, err, ErrChainNotFound)
}

func TestApply_Add_IsADryRunUntilApplied(t *testing.T) {
	h := newHarness(t, filteredNode)

	plan, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: drpc}, false)
	require.NoError(t, err)
	assert.True(t, plan.Changed)
	assert.False(t, plan.Applied)
	assert.Equal(t, []string{drpc}, h.verified, "only the added endpoint is asked")
	require.Len(t, plan.After.Endpoints, 2)
	assert.Equal(t, "sepolia.drpc.org", plan.After.Endpoints[1].Host)
	assert.Zero(t, h.store.replaceWrite+h.store.primaryWrite, "a dry run writes nothing")

	plan, err = h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: drpc}, true)
	require.NoError(t, err)
	assert.True(t, plan.Applied)
	assert.Zero(t, h.store.primaryWrite, "the primary did not change")
	assert.Equal(t, []string{filteredNode, "2:" + drpc}, h.storedList(t))
	assert.True(t, plan.Reloaded)
	assert.Equal(t, filteredNode, h.dialer.primary)
	assert.Equal(t, []chain.RPCFallback{{URL: drpc, Position: 2}}, h.dialer.fallbacks)
}

func TestApply_Add_RefusesAnEndpointOnAnotherNetworkOrThatDoesNotAnswer(t *testing.T) {
	cases := map[string]struct {
		url    string
		reason string
	}{
		"mainnet": {mainnetNode, "serves another network"},
		"silent":  {"https://unreachable.example/rpc", "did not say which network it serves"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, filteredNode)
			_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: tc.url}, true)
			var refusal *Refusal
			require.ErrorAs(t, err, &refusal)
			assert.Contains(t, refusal.Reason, tc.reason)
			assert.Contains(t, refusal.Reason, "nothing written")
			assert.Zero(t, h.store.replaceWrite+h.store.primaryWrite)
		})
	}
}

func TestApply_Add_RefusesARepeatABadPositionAndABadURL(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, true))
	cases := map[string]Change{
		"repeat":       {Action: ActionAdd, URL: drpc + " "},
		"position":     {Action: ActionAdd, URL: tenderly, Position: 4},
		"scheme":       {Action: ActionAdd, URL: "wss://sepolia.drpc.org"},
		"env syntax":   {Action: ActionAdd, URL: "env:lower_case"},
		"env unset":    {Action: ActionAdd, URL: "env:RPC_FALLBACK_TEST_UNSET"},
		"empty":        {Action: ActionAdd},
		"unknown verb": {Action: "rename"},
	}
	for name, change := range cases {
		_, err := h.service.Apply(context.Background(), models.ChainETH, change, true)
		var refusal *Refusal
		assert.ErrorAs(t, err, &refusal, name)
	}
	assert.Empty(t, h.verified)
	assert.Zero(t, h.store.replaceWrite)
}

func TestApply_Add_AtPositionOneMakesANewPrimaryAndKeepsTheOldOneAsAFallback(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, true))

	_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: tenderly, Position: 1}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{tenderly, "2:" + filteredNode, "3:" + drpc}, h.storedList(t))
	assert.Equal(t, 1, h.store.primaryWrite)
}

func TestApply_Remove_EnableAndDisableNeverTouchThePrimary(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, true), fallbackRow(tenderly, 3, true))
	ctx := context.Background()

	for _, action := range []Action{ActionRemove, ActionEnable, ActionDisable} {
		_, err := h.service.Apply(ctx, models.ChainETH, Change{Action: action, Position: 1}, true)
		var refusal *Refusal
		require.ErrorAs(t, err, &refusal, action)
		assert.Contains(t, refusal.Reason, "position 1 is the primary")
	}

	_, err := h.service.Apply(ctx, models.ChainETH, Change{Action: ActionDisable, Position: 2}, true)
	require.NoError(t, err)
	assert.Empty(t, h.verified, "disabling asks nothing")
	assert.Equal(t, []string{filteredNode, "2:disabled:" + drpc, "3:" + tenderly}, h.storedList(t))

	_, err = h.service.Apply(ctx, models.ChainETH, Change{Action: ActionEnable, Position: 2}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{drpc}, h.verified, "an endpoint put back in use is asked again")

	_, err = h.service.Apply(ctx, models.ChainETH, Change{Action: ActionRemove, Position: 2}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{filteredNode, "2:" + tenderly}, h.storedList(t))

	_, err = h.service.Apply(ctx, models.ChainETH, Change{Action: ActionRemove, Position: 3}, true)
	assert.Error(t, err)
}

func TestApply_Order_PromotesAFallbackAfterAskingIt(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, true), fallbackRow(tenderly, 3, false))
	ctx := context.Background()

	_, err := h.service.Apply(ctx, models.ChainETH, Change{Action: ActionOrder, Order: []int{3, 1, 2}}, true)
	var refusal *Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Reason, "disabled endpoint cannot be the primary")

	for _, bad := range [][]int{{2, 1}, {2, 2, 1}, {0, 1, 2}, {2, 1, 4}} {
		_, orderErr := h.service.Apply(ctx, models.ChainETH, Change{Action: ActionOrder, Order: bad}, true)
		assert.ErrorAs(t, orderErr, &refusal, "%v", bad)
	}

	plan, err := h.service.Apply(ctx, models.ChainETH, Change{Action: ActionOrder, Order: []int{2, 3, 1}}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{drpc}, h.verified, "the new primary is asked")
	assert.Equal(t, []string{drpc, "2:disabled:" + tenderly, "3:" + filteredNode}, h.storedList(t))
	assert.Equal(t, 1, h.store.primaryWrite)
	assert.True(t, plan.After.Endpoints[0].Primary)
}

func TestApply_Set_ReplacesTheWholeListAndAsksOnlyTheNewEndpoints(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, true))

	_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionSet, URLs: []string{drpc, tenderly, publicnode}}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{drpc, "2:" + tenderly, "3:" + publicnode}, h.storedList(t))
	assert.ElementsMatch(t, []string{drpc, tenderly, publicnode}, h.verified, "the new primary and the new fallbacks are asked")

	_, err = h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionSet, URLs: []string{drpc, drpc}}, true)
	var refusal *Refusal
	assert.ErrorAs(t, err, &refusal)
	_, err = h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionSet}, true)
	assert.ErrorAs(t, err, &refusal)
}

func TestApply_Defaults_AppendsTheSepoliaListInOrderSkippingWhatIsThere(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(tenderly, 2, true))

	plan, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionDefaults}, true)
	require.NoError(t, err)
	assert.True(t, plan.Changed)
	assert.Equal(t, []string{filteredNode, "2:" + tenderly, "3:" + drpc, "4:" + publicnode}, h.storedList(t))

	plan, err = h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionDefaults}, true)
	require.NoError(t, err)
	assert.False(t, plan.Changed, "a second run changes nothing")
}

func TestApply_Defaults_OfANetworkWithoutAListIsRefused(t *testing.T) {
	h := newHarness(t, filteredNode)
	mainnet := models.EVMNetworkIDEthereumMainnet
	h.store.chains[models.ChainETH].NetworkID = &mainnet

	_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionDefaults}, true)
	var refusal *Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "defaults", refusal.Field)
}

func TestApply_Refuses_EveryChangeOnAChainThatDoesNotFailOverThroughTheList(t *testing.T) {
	h := newHarness(t, filteredNode)

	_, err := h.service.Apply(context.Background(), models.ChainBTC, Change{Action: ActionAdd, URL: "https://blockstream.info/testnet/api"}, true)
	var refusal *Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Reason, "FALLBACK_RPC_URL")

	view, err := h.service.Show(context.Background(), models.ChainBTC, false)
	require.NoError(t, err)
	assert.False(t, view.FailsOver)
	assert.Len(t, view.Endpoints, 1)
}

func TestApply_Nothing_ToChangeAsksAndWritesNothing(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, false))

	plan, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionDisable, Position: 2}, true)
	require.NoError(t, err)
	assert.False(t, plan.Changed)
	assert.False(t, plan.Applied)
	assert.Empty(t, h.verified)
	assert.Zero(t, h.store.replaceWrite)
}

func TestRun_Describes_APlanWithoutAnyURL(t *testing.T) {
	h := newHarness(t, keyedEndpoint)

	report, err := h.service.Run(context.Background(), Request{ChainID: models.ChainETH, Change: &Change{Action: ActionAdd, URL: drpc}})
	require.NoError(t, err)
	text := strings.Join(report.Info, "\n")
	assert.Contains(t, text, "checked position 2 sepolia.drpc.org: serves ethereum-sepolia")
	assert.Contains(t, text, "  after 2 fallback enabled  sepolia.drpc.org")
	assert.Contains(t, text, "dry run: nothing written (pass --apply)")
	assert.NotContains(t, text, "secret-api-key")

	report, err = h.service.Run(context.Background(), Request{ChainID: models.ChainETH, Change: &Change{Action: ActionAdd, URL: mainnetNode}, Apply: true})
	require.Error(t, err)
	assert.Contains(t, strings.Join(report.Warning, "\n"), "position 2 ethereum-rpc.publicnode.com")
}

func TestApply_Recorded_RunsTheRecordInsideTheWriteAndFailsWithIt(t *testing.T) {
	h := newHarness(t, filteredNode)
	var recorded []Plan
	record := func(_ context.Context, plan Plan) error {
		recorded = append(recorded, plan)
		return nil
	}

	plan, err := h.service.ApplyRecorded(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: drpc}, record)
	require.NoError(t, err)
	assert.True(t, plan.Applied)
	require.Len(t, recorded, 1)
	assert.Equal(t, models.ChainETH, recorded[0].Before.ChainID)

	_, err = h.service.ApplyRecorded(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: drpc}, record)
	assert.Error(t, err, "a repeat is refused")
	_, err = h.service.ApplyRecorded(context.Background(), models.ChainETH, Change{Action: ActionDisable, Position: 2}, func(context.Context, Plan) error {
		return errors.New("activity log down")
	})
	assert.EqualError(t, err, "activity log down")
	assert.Len(t, recorded, 1)
}

func TestApply_Reload_HandsTheAdapterTheStoredPositionsOfTheEnabledFallbacks(t *testing.T) {
	h := newHarness(t, filteredNode, fallbackRow(drpc, 2, false), fallbackRow(tenderly, 3, true))

	_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: publicnode}, true)
	require.NoError(t, err)
	assert.Equal(t, []chain.RPCFallback{{URL: tenderly, Position: 3}, {URL: publicnode, Position: 4}}, h.dialer.fallbacks,
		"the disabled position 2 is not dialed and does not shift the others")
}

func TestRun_SkipNetworkCheck_WritesUncheckedAndWarnsAboutEachEndpoint(t *testing.T) {
	h := newHarness(t, filteredNode)
	const silent = "https://unreachable.example/rpc"

	_, err := h.service.Run(context.Background(), Request{ChainID: models.ChainETH, Change: &Change{Action: ActionAdd, URL: silent}, Apply: true})
	require.Error(t, err, "without the flag an endpoint that does not answer is refused")

	h.verified = nil
	report, err := h.service.Run(context.Background(), Request{
		ChainID: models.ChainETH, Change: &Change{Action: ActionAdd, URL: silent}, Apply: true, SkipNetworkCheck: true,
	})
	require.NoError(t, err)
	assert.Empty(t, h.verified, "--skip-network-check asks nothing")
	assert.Equal(t, []string{filteredNode, "2:" + silent}, h.storedList(t))
	assert.Contains(t, strings.Join(report.Warning, "\n"), "position 2 unreachable.example: network NOT checked (--skip-network-check)")
}

func TestApply_Always_ChecksTheNetwork(t *testing.T) {
	h := newHarness(t, filteredNode)
	_, err := h.service.Apply(context.Background(), models.ChainETH, Change{Action: ActionAdd, URL: "https://unreachable.example/rpc"}, true)
	var refusal *Refusal
	assert.ErrorAs(t, err, &refusal, "the platform API (Apply, ApplyRecorded) has no way to skip the check")
}
