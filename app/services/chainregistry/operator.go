package chainregistry

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
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

// AlignerDeps is everything chains:align-network needs. Store is required.
type AlignerDeps struct {
	Store   FullStore
	Decrypt RPCDecrypter
	Probe   NetworkProbe
	Cache   AddressCache
	Profile func() string
}

// Aligner points configured chains at a network profile.
type Aligner struct {
	store   FullStore
	decrypt RPCDecrypter
	probe   NetworkProbe
	cache   AddressCache
	profile func() string
}

// NewAligner wires the alignment command.
func NewAligner(deps AlignerDeps) *Aligner {
	return &Aligner{
		store:   deps.Store,
		decrypt: deps.Decrypt,
		probe:   deps.Probe,
		cache:   deps.Cache,
		profile: deps.Profile,
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
	if !apply {
		report.Info = append(report.Info, "dry run: nothing written (pass --apply)")
		return report, nil
	}
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
