package chains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

// PrimaryVerifier asks an endpoint which network it serves before it becomes a
// chain's primary (chainregistry.PrimaryVerifier). moved is true when that network
// is the one the configured profile moves the chain to, not its own.
type PrimaryVerifier interface {
	Verify(ctx context.Context, record models.Chain, candidate string) (network string, moved bool, err error)
}

// FallbackDisabler turns off the enabled fallbacks of a chain, in the transaction
// Within opens (the chain store joins it through the context), and returns their
// positions.
type FallbackDisabler interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	DisableAll(ctx context.Context, chainID string) ([]int, error)
}

// ReplaceRPCDeps is everything chains:set-rpc needs. Store, Seal and Verify are
// required; Fallbacks is required to move a primary to another network or to skip
// the network check.
type ReplaceRPCDeps struct {
	Store     RPCStore
	Seal      func(plaintext string) (string, error)
	Verify    PrimaryVerifier
	Fallbacks FallbackDisabler
	// Destinations checks the endpoint and what its name resolves to (the RPC guard of
	// the composition root); nil refuses link-local and metadata destinations over the
	// system resolver. A private node is always accepted.
	Destinations destinationGuard
}

// ReplaceRPC writes one chain's sealed rpc_url. The endpoint is never returned.
type ReplaceRPC struct {
	store        RPCStore
	seal         func(plaintext string) (string, error)
	verify       PrimaryVerifier
	fallbacks    FallbackDisabler
	destinations destinationGuard
}

// NewReplaceRPC wires the chain endpoint writer.
func NewReplaceRPC(deps ReplaceRPCDeps) *ReplaceRPC {
	if deps.Destinations == nil {
		deps.Destinations = defaultRPCDestinations()
	}
	return &ReplaceRPC{store: deps.Store, seal: deps.Seal, verify: deps.Verify, fallbacks: deps.Fallbacks, destinations: deps.Destinations}
}

// Replace asks rawURL which network it serves, then seals it and stores it for
// chainID. An endpoint on another network than the chain's (or than the one the
// profile moves the chain to), or one that does not answer, is refused and nothing
// is written; a chain whose endpoints cannot be asked (the Bitcoin family)
// is written unchecked, as before. skipNetworkCheck (artisan --skip-network-check,
// never the platform API) writes without asking, says so in the line and logs a
// warning. When the new primary serves the profile's network instead of the
// chain's (moved) or was not checked, the chain's enabled fallbacks, checked
// against the network it was on, are disabled in the same transaction and the line
// names them. The returned line is the operator message. The endpoint is not part
// of the line, the log or the error.
func (r *ReplaceRPC) Replace(ctx context.Context, chainID, rawURL string, skipNetworkCheck bool) (string, error) {
	chainID = strings.TrimSpace(chainID)
	rawURL = strings.TrimSpace(rawURL)
	if chainID == "" {
		return "", fmt.Errorf("chain_id is required")
	}
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	isEnvReference := strings.HasPrefix(rawURL, models.RPCURLEnvPrefix)
	if isEnvReference && !models.IsValidRPCURLEnvReference(rawURL) {
		return "", fmt.Errorf("env reference must look like env:SOLANA_RPC_URL")
	}
	if !isEnvReference && !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", fmt.Errorf("url must start with http:// or https:// (or be env:NAME)")
	}
	// A link-local or cloud metadata endpoint is refused before it is asked anything,
	// --skip-network-check or not.
	if err := rpcDestination(ctx, r.destinations, rawURL); err != nil {
		return "", fmt.Errorf("url refused: %w", err)
	}
	if r == nil || r.store == nil {
		return "", fmt.Errorf("chain repository is not configured")
	}

	chain, err := r.store.FindByID(ctx, chainID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return "", fmt.Errorf("chain not found: %s", chainID)
		}
		return "", fmt.Errorf("failed to load chain %s: %w", chainID, innermost(err))
	}
	if chain == nil || chain.ID == "" {
		return "", fmt.Errorf("chain not found: %s", chainID)
	}
	note := "WITHOUT a network check (--skip-network-check)"
	otherNetwork := skipNetworkCheck
	if skipNetworkCheck {
		slog.Warn("chain rpc_url written without a network check", "chain", chain.ID, "host", chainpkg.RPCEndpointHost(rawURL))
	} else {
		if r.verify == nil {
			return "", fmt.Errorf("endpoint verifier is not configured")
		}
		network, moved, verifyErr := r.verify.Verify(ctx, *chain, rawURL)
		checked, refused := primaryCheckNote(chain.ID, network, moved, verifyErr)
		if refused != nil {
			return "", refused
		}
		note, otherNetwork = checked, moved
	}
	if otherNetwork && r.fallbacks == nil {
		return "", fmt.Errorf("fallback list is not configured: refusing to write a primary that may serve another network")
	}
	if r.seal == nil {
		return "", fmt.Errorf("failed to encrypt url: sealer is required")
	}
	encURL, err := r.seal(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt url: %w", err)
	}
	var disabled []int
	write := func(ctx context.Context) error {
		if err := r.store.UpdateRPCURL(ctx, chainID, encURL); err != nil {
			if errors.Is(err, models.ErrRepositoryNotFound) {
				return fmt.Errorf("no rows updated for chain %s", chainID)
			}
			return fmt.Errorf("failed to update chain %s: %w", chainID, innermost(err))
		}
		if !otherNetwork {
			return nil
		}
		positions, err := r.fallbacks.DisableAll(ctx, chainID)
		if err != nil {
			return fmt.Errorf("failed to disable the fallbacks of chain %s (nothing written): %w", chainID, err)
		}
		disabled = positions
		return nil
	}
	var err2 error
	if r.fallbacks != nil {
		err2 = r.fallbacks.Within(ctx, write)
	} else {
		err2 = write(ctx)
	}
	if err2 != nil {
		return "", err2
	}
	if len(disabled) > 0 {
		note += fmt.Sprintf("; fallbacks at positions %s disabled: they were checked against the chain's previous network; "+
			"re-enable each with chains:rpc-endpoints %s --enable=N --apply once the chain is aligned", joinPositions(disabled), chainID)
		slog.Warn("chain rpc fallbacks disabled with a primary that may serve another network", "chain", chainID, "positions", disabled)
	}
	return fmt.Sprintf("chain %s rpc_url updated (%s; restart running processes to pick it up)", chainID, note), nil
}

func joinPositions(positions []int) string {
	parts := make([]string, len(positions))
	for i, position := range positions {
		parts[i] = strconv.Itoa(position)
	}
	return strings.Join(parts, ", ")
}

// primaryCheckNote turns what a new primary said about its network into the
// operator note, or into the refusal that keeps it from being written.
func primaryCheckNote(chainID, network string, moved bool, err error) (string, error) {
	switch {
	case err == nil && moved:
		return fmt.Sprintf("serves %s, the network the chain profile names for %s: run chains:align-network --apply to move the chain", network, chainID), nil
	case err == nil:
		return "serves " + network, nil
	case errors.Is(err, chainregistry.ErrEndpointIdentityUnsupported):
		return "network not checked: this chain's endpoints cannot be asked which network they serve", nil
	default:
		return "", fmt.Errorf("refused, nothing written: %w", err)
	}
}

func innermost(err error) error {
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
	return nil
}
