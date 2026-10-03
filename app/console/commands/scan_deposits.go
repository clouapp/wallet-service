package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	return "Scan new blocks for deposits on one configured chain, one block or transaction with --block / --tx, or list / retry the pending blocks with --pending / --retry-pending"
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
			&command.BoolFlag{
				Name:  "pending",
				Usage: "list the blocks whose deposits failed to record and wait for a retry",
			},
			&command.BoolFlag{
				Name:  "retry-pending",
				Usage: "retry every pending block now, ignoring its backoff",
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
	listPending := ctx.OptionBool("pending")
	retryPending := ctx.OptionBool("retry-pending")
	if block < 0 {
		return fmt.Errorf("--block must be positive, got %d", block)
	}
	if selected := countTrue(block > 0, txHash != "", listPending, retryPending); selected > 1 {
		return fmt.Errorf("use only one of --block, --tx, --pending and --retry-pending")
	}

	service := c.deposits
	if service == nil {
		return fmt.Errorf("deposit service is not initialized")
	}

	switch {
	case listPending:
		return printPendingBlocks(ctx, service, chainID)
	case retryPending:
		result, err := service.ReprocessPending(context.Background(), chainID, true)
		if err != nil {
			ctx.Error("pending retry failed: " + err.Error())
			return fmt.Errorf("retry pending %s blocks: %w", chainID, err)
		}
		ctx.Info(fmt.Sprintf("pending retry complete: chain=%s retried=%d resolved=%d still_pending=%d", chainID, result.Due, result.Resolved, result.StillPending))
		if result.StillPending > 0 {
			return fmt.Errorf("%d %s blocks are still pending", result.StillPending, chainID)
		}
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

func printPendingBlocks(ctx console.Context, service *deposit.Service, chainID string) error {
	entries, err := service.ListPending(context.Background(), chainID)
	if err != nil {
		ctx.Error("pending list failed: " + err.Error())
		return fmt.Errorf("list pending %s blocks: %w", chainID, err)
	}
	ctx.Info(fmt.Sprintf("pending blocks: chain=%s count=%d", chainID, len(entries)))
	for _, entry := range entries {
		ctx.Line(fmt.Sprintf("block=%d attempts=%d error_class=%s first_failed=%s last_failed=%s next_retry=%s tx=%s error=%q",
			entry.Block, entry.Attempts, entry.ErrorClass,
			entry.FirstFailedAt.Format(time.RFC3339), entry.LastFailedAt.Format(time.RFC3339), entry.NextRetryAt.Format(time.RFC3339),
			strings.Join(entry.TxHashes, ","), entry.LastError))
	}
	return nil
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
