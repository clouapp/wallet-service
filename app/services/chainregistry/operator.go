package chainregistry

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// Report is what a chain artisan command prints before it fails or returns.
type Report struct {
	Info    []string
	Warning []string
}

// AddressCache rebuilds the watched-address set of one chain.
type AddressCache interface {
	RefreshAddressCache(ctx context.Context, chainID string) error
}

// ScanCheckpoints reads and drops the deposit scan checkpoint of one chain. A
// checkpoint is a block height of the network it was written on, so a chain moved to
// another network starts its scan again at the new network's head.
type ScanCheckpoints interface {
	// Checkpoint returns the chain's checkpoint; found is false for a chain never scanned.
	Checkpoint(ctx context.Context, chainID string) (block uint64, found bool, err error)
	// DropCheckpoint removes the chain's checkpoint and returns the one it removed.
	DropCheckpoint(ctx context.Context, chainID string) (block uint64, found bool, err error)
}

// FallbackEndpoints reads the fallbacks of a chain and disables some of them.
type FallbackEndpoints interface {
	ListByChain(ctx context.Context, chainID string) ([]models.ChainRPCEndpoint, error)
	DisablePositions(ctx context.Context, chainID string, positions []int) error
}

// EndpointVerify asks an endpoint which network it serves (VerifyEndpoint).
type EndpointVerify func(ctx context.Context, adapterType string, target EndpointTarget, rawURL string) (string, error)

// AlignerDeps is everything chains:align-network needs. Store is required.
// Fallbacks, when set, lets a move disable the fallbacks that do not serve the new
// network; Verify defaults to VerifyEndpoint.
type AlignerDeps struct {
	Store       FullStore
	Decrypt     RPCDecrypter
	Probe       NetworkProbe
	Cache       AddressCache
	Checkpoints ScanCheckpoints
	Fallbacks   FallbackEndpoints
	Verify      EndpointVerify
	Profile     func() string
}

// Aligner points configured chains at a network profile.
type Aligner struct {
	store       FullStore
	decrypt     RPCDecrypter
	probe       NetworkProbe
	cache       AddressCache
	checkpoints ScanCheckpoints
	fallbacks   FallbackEndpoints
	verify      EndpointVerify
	profile     func() string
}

// NewAligner wires the alignment command.
func NewAligner(deps AlignerDeps) *Aligner {
	verify := deps.Verify
	if verify == nil {
		verify = VerifyEndpoint
	}
	return &Aligner{
		store:       deps.Store,
		decrypt:     deps.Decrypt,
		probe:       deps.Probe,
		cache:       deps.Cache,
		checkpoints: deps.Checkpoints,
		fallbacks:   deps.Fallbacks,
		verify:      verify,
		profile:     deps.Profile,
	}
}

// Align plans, and with apply writes, the move onto profile.
func (a *Aligner) Align(ctx context.Context, profile, accountRaw string, apply bool) (Report, error) {
	var report Report
	if a == nil || a.store == nil {
		return report, fmt.Errorf("chainregistry: store is required")
	}
	profile = strings.TrimSpace(profile)
	if profile == "" && a.profile != nil {
		profile = strings.TrimSpace(a.profile())
	}
	if profile == "" {
		return report, fmt.Errorf("no profile: pass --profile=mainnet|testnet or set CHAIN_NETWORK_PROFILE")
	}
	var accountID uuid.UUID
	if raw := strings.TrimSpace(accountRaw); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return report, fmt.Errorf("invalid --account %q: %w", raw, err)
		}
		accountID = parsed
	}
	alignment, err := PlanAlignment(ctx, profile, a.store, a.decrypt, a.probe, accountID)
	if err != nil {
		return report, err
	}
	report = describeAlignment(alignment)
	if alignment.IsEmpty() {
		return report, nil
	}
	moved := networkMoves(alignment.Plan)
	stale, err := a.staleFallbacks(ctx, moved)
	if err != nil {
		return report, err
	}
	if !apply {
		a.describeCheckpointDrops(ctx, &report, moved)
		describeFallbackDrops(&report, stale, "would be disabled")
		report.Info = append(report.Info, "dry run: nothing written (pass --apply)")
		return report, nil
	}
	// The checkpoints and the stale fallbacks go first: a drop that fails (a scan
	// holds the chain) stops the run before any chain moves, so running it again
	// plans the same moves and drops again.
	if err := a.dropCheckpoints(ctx, &report, moved); err != nil {
		return report, err
	}
	if err := a.disableFallbacks(ctx, stale); err != nil {
		return report, err
	}
	describeFallbackDrops(&report, stale, "disabled")
	if err := ApplyAlignment(ctx, a.store, alignment); err != nil {
		return report, err
	}
	for chainID := range alignment.Reissues {
		if a.cache == nil {
			report.Warning = append(report.Warning, fmt.Sprintf("address cache of %s not refreshed: no deposit service", chainID))
			continue
		}
		if err := a.cache.RefreshAddressCache(ctx, chainID); err != nil {
			report.Warning = append(report.Warning, fmt.Sprintf("address cache of %s not refreshed: %s", chainID, err))
		}
	}
	report.Info = append(report.Info, "applied; restart the API and workers so the chain adapters reload")
	return report, nil
}

// staleFallback is an enabled fallback of a moved chain that does not serve the
// network it moves to (or does not say).
type staleFallback struct {
	chainID  string
	position int
	host     string
	network  string
}

// staleFallbacks asks every enabled fallback of each moved chain which network it
// serves and returns those that do not serve the chain's new network.
func (a *Aligner) staleFallbacks(ctx context.Context, moves []ChainChange) ([]staleFallback, error) {
	if a.fallbacks == nil {
		return nil, nil
	}
	var stale []staleFallback
	for _, change := range moves {
		rows, err := a.fallbacks.ListByChain(ctx, change.ChainID)
		if err != nil {
			return nil, fmt.Errorf("read the fallbacks of %s (no chain was changed): %w", change.ChainID, err)
		}
		goal := EndpointTarget{Network: change.ToNetwork}
		if change.ToNetworkID != nil {
			goal.EVMNetworkID = *change.ToNetworkID
		}
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			endpoint := ""
			if a.decrypt != nil {
				if opened, openErr := a.decrypt(row.URL); openErr == nil {
					endpoint = opened
				}
			}
			if endpoint != "" {
				if _, verifyErr := a.verify(ctx, change.AdapterType, goal, endpoint); verifyErr == nil {
					continue
				}
			}
			stale = append(stale, staleFallback{chainID: change.ChainID, position: row.Position, host: chain.RPCEndpointHost(endpoint), network: change.ToNetwork})
		}
	}
	return stale, nil
}

// disableFallbacks disables the stale fallbacks, chain by chain.
func (a *Aligner) disableFallbacks(ctx context.Context, stale []staleFallback) error {
	byChain := map[string][]int{}
	var order []string
	for _, fallback := range stale {
		if _, seen := byChain[fallback.chainID]; !seen {
			order = append(order, fallback.chainID)
		}
		byChain[fallback.chainID] = append(byChain[fallback.chainID], fallback.position)
	}
	for _, chainID := range order {
		if err := a.fallbacks.DisablePositions(ctx, chainID, byChain[chainID]); err != nil {
			return fmt.Errorf("disable the fallbacks of %s that do not serve its new network (no chain was changed; run the command again): %w", chainID, err)
		}
	}
	return nil
}

func describeFallbackDrops(report *Report, stale []staleFallback, verb string) {
	for _, fallback := range stale {
		host := fallback.host
		if host == "" {
			host = "(does not open)"
		}
		report.Info = append(report.Info, fmt.Sprintf("chain %s: fallback %d %s %s: it does not serve %s", fallback.chainID, fallback.position, host, verb, fallback.network))
	}
}

// networkMoves are the changes that put a chain on another network, not only in
// another environment list.
func networkMoves(plan *Plan) []ChainChange {
	var moves []ChainChange
	for _, change := range plan.Changes {
		if change.NetworkChanges() {
			moves = append(moves, change)
		}
	}
	return moves
}

// describeCheckpointDrops reports, in a dry run, the checkpoint each move would drop.
func (a *Aligner) describeCheckpointDrops(ctx context.Context, report *Report, moves []ChainChange) {
	for _, change := range moves {
		if a.checkpoints == nil {
			report.Warning = append(report.Warning, fmt.Sprintf("deposit scan checkpoint of %s would not be dropped: no deposit service", change.ChainID))
			continue
		}
		block, found, err := a.checkpoints.Checkpoint(ctx, change.ChainID)
		switch {
		case err != nil:
			report.Warning = append(report.Warning, fmt.Sprintf("deposit scan checkpoint of %s not read: %s", change.ChainID, err))
			report.Info = append(report.Info, fmt.Sprintf("chain %s: its deposit scan checkpoint would be dropped; the next scan starts at the head of %s", change.ChainID, change.ToNetwork))
		case found:
			report.Info = append(report.Info, fmt.Sprintf("chain %s: deposit scan checkpoint %d would be dropped; the next scan starts at the head of %s", change.ChainID, block, change.ToNetwork))
		default:
			report.Info = append(report.Info, fmt.Sprintf("chain %s: no deposit scan checkpoint; the next scan starts at the head of %s", change.ChainID, change.ToNetwork))
		}
	}
}

// dropCheckpoints drops the checkpoint of every chain the alignment moves to another
// network: its block is a height of the old network. It fails on the first chain whose
// checkpoint cannot be dropped.
func (a *Aligner) dropCheckpoints(ctx context.Context, report *Report, moves []ChainChange) error {
	for _, change := range moves {
		if a.checkpoints == nil {
			report.Warning = append(report.Warning, fmt.Sprintf("deposit scan checkpoint of %s not dropped: no deposit service; move it with scan:deposits %s --set-checkpoint=<head> --apply", change.ChainID, change.ChainID))
			continue
		}
		block, found, err := a.checkpoints.DropCheckpoint(ctx, change.ChainID)
		if err != nil {
			return fmt.Errorf("drop the deposit scan checkpoint of %s (no chain was changed; run the command again): %w", change.ChainID, err)
		}
		if !found {
			report.Info = append(report.Info, fmt.Sprintf("chain %s: no deposit scan checkpoint; the next scan starts at the head of %s", change.ChainID, change.ToNetwork))
			continue
		}
		report.Info = append(report.Info, fmt.Sprintf("chain %s: deposit scan checkpoint %d dropped; the next scan starts at the head of %s (if the RPC already served %s, restore it with scan:deposits %s --set-checkpoint=%d --apply)",
			change.ChainID, block, change.ToNetwork, change.ToNetwork, change.ChainID, block))
	}
	return nil
}

func describeAlignment(alignment *Alignment) Report {
	var report Report
	plan := alignment.Plan
	for _, warning := range plan.Warnings {
		report.Warning = append(report.Warning, warning)
	}
	if alignment.IsEmpty() {
		report.Info = append(report.Info, fmt.Sprintf("already aligned to the %s profile", plan.Profile))
		return report
	}
	for _, change := range plan.Changes {
		report.Info = append(report.Info, fmt.Sprintf("chain %s: network %q → %q, network_id %s → %s, is_testnet %t → %t",
			change.ChainID, change.FromNetwork, change.ToNetwork,
			formatNetworkID(change.FromNetworkID), formatNetworkID(change.ToNetworkID),
			change.FromTestnet, change.ToTestnet))
		for _, reissue := range alignment.Reissues[change.ChainID] {
			report.Info = append(report.Info, fmt.Sprintf("  wallet %s (%s): retire %d address(es), genesis %s",
				reissue.WalletID, reissue.WalletLabel, len(reissue.Retired), formatGenesis(reissue.Genesis.Address)))
		}
	}
	if change := alignment.AccountChange; change != nil {
		report.Info = append(report.Info, fmt.Sprintf("account %s: environment %s → %s", change.AccountID, change.From, change.To))
	}
	return report
}

func formatNetworkID(id *int64) string {
	if id == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *id)
}

func formatGenesis(address string) string {
	if address == "" {
		return "unchanged"
	}
	return address
}

// AddedChain is a record the seeder would create.
type AddedChain struct {
	ID          string
	AdapterType string
	EnvVar      string
	Network     string
	NetworkID   *int64
	IsTestnet   bool
}

// AddedChainsPlan is what the seeder created, or would create.
type AddedChainsPlan struct {
	Chains    []AddedChain
	Tokens    []string
	Resources []string
	Skipped   []string
}

// MissingSeeder reports or creates the added EVM chain records.
type MissingSeeder func(ctx context.Context, apply bool) (AddedChainsPlan, error)

// MissingChains creates chain records a live registry lacks.
type MissingChains struct {
	seed MissingSeeder
}

// NewMissingChains wires the seeder.
func NewMissingChains(seed MissingSeeder) *MissingChains {
	if seed == nil {
		panic("chains:add-missing: seeder is required")
	}
	return &MissingChains{seed: seed}
}

// Add previews the missing chains and, when apply is set, creates them.
func (m *MissingChains) Add(ctx context.Context, apply bool) (Report, error) {
	var report Report
	if m == nil || m.seed == nil {
		return report, fmt.Errorf("chains:add-missing: seeder is required")
	}
	plan, err := m.seed(ctx, false)
	if err != nil {
		return report, err
	}
	for _, id := range plan.Skipped {
		report.Info = append(report.Info, fmt.Sprintf("%s: already in the registry, left as is", id))
	}
	if len(plan.Chains) == 0 {
		report.Info = append(report.Info, "nothing to add")
		return report, nil
	}
	for _, added := range plan.Chains {
		if err := checkAddedChainRPC(ctx, added); err != nil {
			return report, err
		}
		report.Info = append(report.Info, fmt.Sprintf("%s: create on %s (network_id %s, is_testnet %t), rpc_url env:%s",
			added.ID, added.Network, formatNetworkID(added.NetworkID), added.IsTestnet, added.EnvVar))
	}
	for _, token := range plan.Tokens {
		report.Info = append(report.Info, "token "+token)
	}
	for _, resource := range plan.Resources {
		report.Info = append(report.Info, "resource "+resource)
	}
	if !apply {
		report.Info = append(report.Info, "dry run: nothing written (pass --apply)")
		return report, nil
	}
	if _, err := m.seed(ctx, true); err != nil {
		return report, err
	}
	report.Info = append(report.Info, fmt.Sprintf("created %d chains; restart the API so it registers their adapters", len(plan.Chains)))
	return report, nil
}

func checkAddedChainRPC(ctx context.Context, added AddedChain) error {
	rpcURL, err := models.ResolveRPCURL(models.RPCURLEnvPrefix + added.EnvVar)
	if err != nil {
		return fmt.Errorf("%s: %w", added.ID, err)
	}
	record := models.Chain{ID: added.ID, AdapterType: added.AdapterType}
	served, err := ProbeRPCNetwork(ctx, record, rpcURL)
	if err != nil {
		return fmt.Errorf("%s: probe %s: %w", added.ID, added.EnvVar, err)
	}
	if !strings.EqualFold(served, added.Network) {
		return fmt.Errorf("%w: %s serves %q, %s needs %q", ErrRPCNetworkMismatch, added.EnvVar, served, added.ID, added.Network)
	}
	return nil
}
