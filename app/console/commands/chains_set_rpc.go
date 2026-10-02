package commands

import (
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type ChainsSetRPC struct{}

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
	chainID := strings.TrimSpace(ctx.ArgumentString("chain_id"))
	rawURL := strings.TrimSpace(ctx.ArgumentString("url"))

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

	var existing models.Chain
	if err := facades.Orm().Query().Where("id", chainID).First(&existing); err != nil {
		ctx.Error(fmt.Sprintf("failed to load chain %s: %s", chainID, err.Error()))
		return fmt.Errorf("load chain %s: %w", chainID, err)
	}
	if existing.ID == "" {
		ctx.Error(fmt.Sprintf("chain not found: %s", chainID))
		return fmt.Errorf("chain not found: %s", chainID)
	}

	encURL, err := facades.Crypt().EncryptString(rawURL)
	if err != nil {
		ctx.Error("failed to encrypt url: " + err.Error())
		return fmt.Errorf("encrypt url: %w", err)
	}

	result, err := facades.Orm().Query().
		Model(&models.Chain{}).
		Where("id = ?", chainID).
		Update("rpc_url", encURL)
	if err != nil {
		ctx.Error(fmt.Sprintf("failed to update chain %s: %s", chainID, err.Error()))
		return fmt.Errorf("update chain %s: %w", chainID, err)
	}
	if result.RowsAffected == 0 {
		ctx.Error(fmt.Sprintf("no rows updated for chain %s", chainID))
		return fmt.Errorf("no rows updated for chain %s", chainID)
	}

	ctx.Info(fmt.Sprintf("chain %s rpc_url updated (restart running processes to pick it up)", chainID))
	return nil
}
