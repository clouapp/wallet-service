package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

type ScanDeposits struct {
	deposits *deposit.Service
}

// NewScanDeposits scans deposits for one chain.
func NewScanDeposits(deposits *deposit.Service) *ScanDeposits {
	if deposits == nil {
		panic("scan:deposits: deposit service is required")
	}
	return &ScanDeposits{deposits: deposits}
}

func (c *ScanDeposits) Signature() string {
	return "scan:deposits"
}

func (c *ScanDeposits) Description() string {
	return "Scan new blocks for deposits on one configured chain, or one block or transaction with --block / --tx"
}

func (c *ScanDeposits) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "chain",
				Usage:    "configured chain id, for example teth",
				Required: true,
			},
		},
		Flags: []command.Flag{
			&command.Int64Flag{
				Name:    "block",
				Aliases: []string{"slot"},
				Usage:   "record the deposits of this block (Solana slot) only; the checkpoint is not moved",
			},
			&command.StringFlag{
				Name:    "tx",
				Aliases: []string{"signature"},
				Usage:   "record the deposits of this finalized transaction only; the checkpoint is not moved",
			},
		},
	}
}

func (c *ScanDeposits) Handle(ctx console.Context) error {
	chainID := strings.TrimSpace(ctx.ArgumentString("chain"))
	if chainID == "" {
		return fmt.Errorf("chain is required")
	}
	block := ctx.OptionInt64("block")
	txHash := strings.TrimSpace(ctx.Option("tx"))
	if block < 0 {
		return fmt.Errorf("--block must be positive, got %d", block)
	}
	if block > 0 && txHash != "" {
		return fmt.Errorf("use either --block or --tx, not both")
	}

	service := c.deposits
	if service == nil {
		return fmt.Errorf("deposit service is not initialized")
	}

	switch {
	case block > 0:
		recorded, err := service.ScanBlock(context.Background(), chainID, uint64(block))
		if err != nil {
			ctx.Error("block scan failed: " + err.Error())
			return fmt.Errorf("scan %s block %d: %w", chainID, block, err)
		}
		ctx.Info("block scan complete: chain=" + chainID + " block=" + strconv.FormatInt(block, 10) + " new_deposits=" + strconv.Itoa(recorded))
	case txHash != "":
		recorded, err := service.ScanTransaction(context.Background(), chainID, txHash)
		if err != nil {
			ctx.Error("transaction scan failed: " + err.Error())
			return fmt.Errorf("scan %s transaction %s: %w", chainID, txHash, err)
		}
		ctx.Info("transaction scan complete: chain=" + chainID + " tx=" + txHash + " new_deposits=" + strconv.Itoa(recorded))
	default:
		if err := service.ScanLatestBlocks(context.Background(), chainID); err != nil {
			ctx.Error("deposit scan failed: " + err.Error())
			return fmt.Errorf("scan deposits for %s: %w", chainID, err)
		}
		ctx.Info("deposit scan complete: chain=" + chainID)
	}
	return nil
}
