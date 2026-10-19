package chains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// ReplaceRPCDeps is everything chains:set-rpc needs. Store, Seal and Verify are required.
type ReplaceRPCDeps struct {
	Store  RPCStore
	Seal   func(plaintext string) (string, error)
	Verify PrimaryVerifier
}

// ReplaceRPC writes one chain's sealed rpc_url. The endpoint is never returned.
type ReplaceRPC struct {
	store  RPCStore
	seal   func(plaintext string) (string, error)
	verify PrimaryVerifier
}

// NewReplaceRPC wires the chain endpoint writer.
func NewReplaceRPC(deps ReplaceRPCDeps) *ReplaceRPC {
	return &ReplaceRPC{store: deps.Store, seal: deps.Seal, verify: deps.Verify}
}

// Replace asks rawURL which network it serves, then seals it and stores it for
// chainID. An endpoint on another network than the chain's (or than the one the
// profile moves the chain to), or one that does not answer, is refused and nothing
// is written; a chain whose endpoints cannot be asked (Bitcoin family, TRON, XRP)
// is written unchecked, as before. skipNetworkCheck (artisan --skip-network-check,
// never the platform API) writes without asking, says so in the line and logs a
// warning. The returned line is the operator message. The endpoint is not part of
// the line, the log or the error.
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
		note = checked
	}
	if r.seal == nil {
		return "", fmt.Errorf("failed to encrypt url: sealer is required")
	}
	encURL, err := r.seal(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt url: %w", err)
	}
	if err := r.store.UpdateRPCURL(ctx, chainID, encURL); err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return "", fmt.Errorf("no rows updated for chain %s", chainID)
		}
		return "", fmt.Errorf("failed to update chain %s: %w", chainID, innermost(err))
	}
	return fmt.Sprintf("chain %s rpc_url updated (%s; restart running processes to pick it up)", chainID, note), nil
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
