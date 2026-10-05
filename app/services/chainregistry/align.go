// Package chainregistry keeps the primary chain records (eth, btc, polygon, sol)
// on the networks a chain network profile names, and an account's environment in
// line with them.
package chainregistry

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Store is the persistence the alignment needs.
type Store interface {
	Chains(ctx context.Context) ([]models.Chain, error)
	// ChainHoldsBalance reports whether any wallet on the chain has a non-zero cached balance.
	ChainHoldsBalance(ctx context.Context, chainID string) (bool, error)
	UpdateChainNetwork(ctx context.Context, chainID string, networkID *int64, isTestnet bool) error
	FindAccount(ctx context.Context, id uuid.UUID) (*models.Account, error)
	UpdateAccountEnvironment(ctx context.Context, id uuid.UUID, environment string) error
}

// RPCDecrypter turns a stored rpc_url into the plaintext URL.
type RPCDecrypter func(encrypted string) (string, error)

// NetworkProbe names the network an RPC URL serves, or "" when it cannot tell.
// A nil probe skips the check.
type NetworkProbe func(ctx context.Context, chain models.Chain, rpcURL string) (string, error)

// ErrFundedChainNetworkChange stops a plan that would move a chain holding funds to
// another network: its wallets would sign for, and watch, a network their funds are not on.
var ErrFundedChainNetworkChange = errors.New("chain holds balances; refusing to change its network")

// ErrRPCNetworkMismatch stops a plan whose target network is not the one the
// chain's rpc_url serves.
var ErrRPCNetworkMismatch = errors.New("rpc_url serves a different network")

// ChainChange is one primary record that the profile moves.
type ChainChange struct {
	ChainID       string
	AdapterType   string
	FromNetwork   string
	ToNetwork     string
	FromNetworkID *int64
	ToNetworkID   *int64
	FromTestnet   bool
	ToTestnet     bool
}

// NetworkChanges reports whether the record ends up on another network, not only
// in another environment list.
func (c ChainChange) NetworkChanges() bool { return c.FromNetwork != c.ToNetwork }

// Plan is what aligning the registry to Profile would change.
type Plan struct {
	Profile  string
	Changes  []ChainChange
	Warnings []string
}

// BuildPlan compares every primary record with profile. It fails without changing
// anything when a record holding balances would change network, or when probe shows
// the record's rpc_url serving another network than the profile names.
func BuildPlan(ctx context.Context, profile string, store Store, decrypt RPCDecrypter, probe NetworkProbe) (*Plan, error) {
	if !models.IsChainNetworkProfile(profile) {
		return nil, fmt.Errorf("unknown chain network profile %q (want %q or %q)",
			profile, models.ChainNetworkProfileMainnet, models.ChainNetworkProfileTestnet)
	}
	if store == nil || decrypt == nil {
		return nil, errors.New("chainregistry: store and decrypter are required")
	}

	chains, err := store.Chains(ctx)
	if err != nil {
		return nil, fmt.Errorf("load chains: %w", err)
	}
	byID := make(map[string]models.Chain, len(chains))
	for _, ch := range chains {
		byID[ch.ID] = ch
	}

	plan := &Plan{Profile: profile}
	for _, chainID := range models.PrimaryChainIDs {
		current, found := byID[chainID]
		if !found {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("chain %s is not in the registry; the seeder or chains:add-missing creates it", chainID))
			continue
		}
		change, warning, err := planChain(ctx, profile, current, store, decrypt, probe)
		if err != nil {
			return nil, err
		}
		if warning != "" {
			plan.Warnings = append(plan.Warnings, warning)
		}
		if change != nil {
			plan.Changes = append(plan.Changes, *change)
		}
	}
	return plan, nil
}

func planChain(ctx context.Context, profile string, current models.Chain, store Store, decrypt RPCDecrypter, probe NetworkProbe) (*ChainChange, string, error) {
	spec, _, err := models.PrimaryChainNetwork(profile, current.ID)
	if err != nil {
		return nil, "", err
	}
	rpcURL, err := decrypt(current.RpcURL)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt rpc_url of %s: %w", current.ID, err)
	}

	warning, err := checkRPCServes(ctx, current, rpcURL, spec, probe)
	if err != nil {
		return nil, "", err
	}

	target := current
	target.NetworkID = spec.NetworkID
	target.IsTestnet = spec.IsTestnet
	from := current.ResolveNetwork(rpcURL)
	to := target.ResolveNetwork(rpcURL)
	if !spec.Accepts(to.Name) {
		return nil, "", fmt.Errorf("%w: %s would resolve to %q, not %q; point its rpc_url at %s first (chains:set-rpc)",
			ErrRPCNetworkMismatch, current.ID, to.Name, spec.Network, spec.Network)
	}

	if sameNetworkID(current.NetworkID, spec.NetworkID) && current.IsTestnet == spec.IsTestnet {
		return nil, warning, nil
	}

	change := &ChainChange{
		ChainID:       current.ID,
		AdapterType:   current.AdapterType,
		FromNetwork:   from.Name,
		ToNetwork:     to.Name,
		FromNetworkID: current.NetworkID,
		ToNetworkID:   spec.NetworkID,
		FromTestnet:   current.IsTestnet,
		ToTestnet:     spec.IsTestnet,
	}
	if change.NetworkChanges() {
		funded, err := store.ChainHoldsBalance(ctx, current.ID)
		if err != nil {
			return nil, "", fmt.Errorf("check balances on %s: %w", current.ID, err)
		}
		if funded {
			return nil, "", fmt.Errorf("%w: %s (%s → %s)", ErrFundedChainNetworkChange, current.ID, from.Name, to.Name)
		}
	}
	return change, warning, nil
}

func checkRPCServes(ctx context.Context, current models.Chain, rpcURL string, spec models.ChainNetworkSpec, probe NetworkProbe) (string, error) {
	if probe == nil {
		return "", nil
	}
	served, err := probe(ctx, current, rpcURL)
	if err != nil {
		return "", fmt.Errorf("probe rpc_url of %s: %w", current.ID, err)
	}
	if served == "" {
		return fmt.Sprintf("could not tell which network the rpc_url of %s serves; check it points at %s", current.ID, spec.Network), nil
	}
	if !spec.Accepts(served) {
		return "", fmt.Errorf("%w: rpc_url of %s serves %q, not %q; run chains:set-rpc first",
			ErrRPCNetworkMismatch, current.ID, served, spec.Network)
	}
	return "", nil
}

// ApplyPlan writes every change of plan. It is idempotent: an applied plan rebuilt
// against the same profile has no changes.
func ApplyPlan(ctx context.Context, store Store, plan *Plan) error {
	if store == nil || plan == nil {
		return errors.New("chainregistry: store and plan are required")
	}
	for _, change := range plan.Changes {
		if err := store.UpdateChainNetwork(ctx, change.ChainID, change.ToNetworkID, change.ToTestnet); err != nil {
			return fmt.Errorf("update chain %s: %w", change.ChainID, err)
		}
	}
	return nil
}

// AccountChange is an account environment the profile moves.
type AccountChange struct {
	AccountID uuid.UUID
	From      string
	To        string
}

// PlanAccountEnvironment returns the change that puts the account in the environment
// whose chain list holds the primary records under profile, or nil when it already is.
// A paired account must stay in the other environment, so a pair that would end up
// with both sides in the same environment is refused.
func PlanAccountEnvironment(ctx context.Context, store Store, accountID uuid.UUID, profile string) (*AccountChange, error) {
	if accountID == uuid.Nil {
		return nil, errors.New("account id is required")
	}
	if store == nil {
		return nil, errors.New("chainregistry: store is required")
	}
	want, err := models.EnvironmentForChainNetworkProfile(profile)
	if err != nil {
		return nil, err
	}
	account, err := store.FindAccount(ctx, accountID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("account %s not found", accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("load account %s: %w", accountID, err)
	}
	if account.Environment == want {
		return nil, nil
	}
	if account.LinkedAccountID != nil {
		linked, err := store.FindAccount(ctx, *account.LinkedAccountID)
		if errors.Is(err, models.ErrRepositoryNotFound) {
			linked = nil
		} else if err != nil {
			return nil, fmt.Errorf("load linked account %s: %w", *account.LinkedAccountID, err)
		}
		if linked != nil && linked.Environment == want {
			return nil, fmt.Errorf("account %s is paired with %s, which is already %q", accountID, linked.ID, want)
		}
	}
	return &AccountChange{AccountID: accountID, From: account.Environment, To: want}, nil
}

// ApplyAccountChange writes change; a nil change is a no-op.
func ApplyAccountChange(ctx context.Context, store Store, change *AccountChange) error {
	if change == nil {
		return nil
	}
	if store == nil {
		return errors.New("chainregistry: store is required")
	}
	return store.UpdateAccountEnvironment(ctx, change.AccountID, change.To)
}

func sameNetworkID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
