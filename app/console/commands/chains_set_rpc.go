package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// chainRPCStore is the chain repository chains:set-rpc writes through.
// Bootstrap passes the bound repository; this file does not import it.
type chainRPCStore interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
	UpdateRPCURL(ctx context.Context, id, sealed string) error
}

// chainRPCConsole is the slice of the artisan context this command prints to.
type chainRPCConsole interface {
	Error(message string)
	Info(message string)
}

// ChainsSetRPC replaces the sealed rpc_url of one chain. The endpoint is
// encrypted before it is stored and is never printed.
type ChainsSetRPC struct {
	chains chainRPCStore
	// encrypt seals the endpoint. Nil uses the process cipher.
	encrypt func(plaintext string) (string, error)
}

// NewChainsSetRPC wires the command to the chain repository.
func NewChainsSetRPC(chains chainRPCStore) *ChainsSetRPC {
	return &ChainsSetRPC{chains: chains}
}

func (c *ChainsSetRPC) Signature() string {
	return "chains:set-rpc"
}

func (c *ChainsSetRPC) Description() string {
	return "Update the encrypted rpc_url of a chain row (e.g. after rotating providers)"
}

func (c *ChainsSetRPC) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "chain_id",
				Usage:    "chain identifier (e.g. eth, teth, polygon, sol)",
				Required: true,
			},
			&command.ArgumentString{
				Name:     "url",
				Usage:    "new RPC URL, or env:NAME to read it from an environment variable (encrypted at rest)",
				Required: true,
			},
		},
	}
}

func (c *ChainsSetRPC) Handle(ctx console.Context) error {
	return c.setRPC(ctx, ctx.ArgumentString("chain_id"), ctx.ArgumentString("url"))
}

func (c *ChainsSetRPC) setRPC(ctx chainRPCConsole, chainID, rawURL string) error {
	chainID = strings.TrimSpace(chainID)
	rawURL = strings.TrimSpace(rawURL)

	if chainID == "" {
		ctx.Error("chain_id is required")
		return fmt.Errorf("chain_id is required")
	}
	if rawURL == "" {
		ctx.Error("url is required")
		return fmt.Errorf("url is required")
	}
	isEnvReference := strings.HasPrefix(rawURL, models.RPCURLEnvPrefix)
	if isEnvReference && !models.IsValidRPCURLEnvReference(rawURL) {
		ctx.Error("env reference must look like env:SOLANA_RPC_URL")
		return fmt.Errorf("invalid env reference")
	}
	if !isEnvReference && !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		ctx.Error("url must start with http:// or https:// (or be env:NAME)")
		return fmt.Errorf("invalid url scheme")
	}
	if c == nil || c.chains == nil {
		ctx.Error("chain repository is not configured")
		return fmt.Errorf("chain repository is not configured")
	}

	chain, err := c.chains.FindByID(context.Background(), chainID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			ctx.Error(fmt.Sprintf("chain not found: %s", chainID))
			return fmt.Errorf("chain not found: %s", chainID)
		}
		cause := chainRPCCause(err)
		ctx.Error(fmt.Sprintf("failed to load chain %s: %s", chainID, cause))
		return fmt.Errorf("load chain %s: %w", chainID, cause)
	}
	if chain == nil || chain.ID == "" {
		ctx.Error(fmt.Sprintf("chain not found: %s", chainID))
		return fmt.Errorf("chain not found: %s", chainID)
	}

	encURL, err := c.seal(rawURL)
	if err != nil {
		ctx.Error("failed to encrypt url: " + err.Error())
		return fmt.Errorf("encrypt url: %w", err)
	}

	if err := c.chains.UpdateRPCURL(context.Background(), chainID, encURL); err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			ctx.Error(fmt.Sprintf("no rows updated for chain %s", chainID))
			return fmt.Errorf("no rows updated for chain %s", chainID)
		}
		cause := chainRPCCause(err)
		ctx.Error(fmt.Sprintf("failed to update chain %s: %s", chainID, cause))
		return fmt.Errorf("update chain %s: %w", chainID, cause)
	}

	ctx.Info(fmt.Sprintf("chain %s rpc_url updated (restart running processes to pick it up)", chainID))
	return nil
}

func (c *ChainsSetRPC) seal(plaintext string) (string, error) {
	if c != nil && c.encrypt != nil {
		return c.encrypt(plaintext)
	}
	return facades.Crypt().EncryptString(plaintext)
}

// chainRPCCause is the innermost error. The repository wraps the query name
// around a database failure; the command keeps the text it printed before
// that wrap existed. The endpoint is not part of either text.
func chainRPCCause(err error) error {
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
	return nil
}
