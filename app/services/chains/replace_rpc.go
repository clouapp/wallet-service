package chains

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// ReplaceRPCDeps is everything chains:set-rpc needs. Store and Seal are required.
type ReplaceRPCDeps struct {
	Store RPCStore
	Seal  func(plaintext string) (string, error)
}

// ReplaceRPC writes one chain's sealed rpc_url. The endpoint is never returned.
type ReplaceRPC struct {
	store RPCStore
	seal  func(plaintext string) (string, error)
}

// NewReplaceRPC wires the chain endpoint writer.
func NewReplaceRPC(deps ReplaceRPCDeps) *ReplaceRPC {
	return &ReplaceRPC{store: deps.Store, seal: deps.Seal}
}

// Replace seals rawURL and stores it for chainID. The returned line is the
// operator message. The endpoint is not part of the line or the error.
func (r *ReplaceRPC) Replace(ctx context.Context, chainID, rawURL string) (string, error) {
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
	return fmt.Sprintf("chain %s rpc_url updated (restart running processes to pick it up)", chainID), nil
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
