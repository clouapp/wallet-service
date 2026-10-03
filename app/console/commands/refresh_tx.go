package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/refresh"
)

type RefreshTx struct {
	balances *refresh.BalanceService
}

// NewRefreshTx refreshes the wallet that owns one transaction.
func NewRefreshTx(balances *refresh.BalanceService) *RefreshTx {
	if balances == nil {
		panic("refresh:tx: balance refresh service is required")
	}
	return &RefreshTx{balances: balances}
}

func (c *RefreshTx) Signature() string {
	return "refresh:tx"
}

func (c *RefreshTx) Description() string {
	return "Refresh read model for a single transaction"
}

func (c *RefreshTx) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "chain",
				Usage:    "blockchain identifier (e.g. ethereum, bitcoin, solana)",
				Required: true,
			},
			&command.ArgumentString{
				Name:     "tx_hash",
				Usage:    "transaction hash to refresh",
				Required: true,
			},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshTx) Handle(ctx console.Context) error {
	chain := ctx.ArgumentString("chain")
	txHash := ctx.ArgumentString("tx_hash")
	reason := ctx.Option("reason")

	tx, err := container.MustMake[*repositories.TransactionRepository]().FindByChainAndTxHash(context.Background(), chain, txHash)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		ctx.Error("failed to look up transaction: " + err.Error())
		return fmt.Errorf("look up transaction: %w", err)
	}
	if err != nil || tx == nil {
		ctx.Error("transaction not found: chain=" + chain + " tx_hash=" + txHash)
		return fmt.Errorf("transaction not found: chain=%s tx_hash=%s", chain, txHash)
	}

	wallet, err := container.MustMake[*repositories.WalletRepository]().FindByID(context.Background(), tx.WalletID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		ctx.Error("failed to load wallet for transaction: " + err.Error())
		return fmt.Errorf("load wallet for tx %s: %w", txHash, err)
	}
	if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		ctx.Error("wallet not found for transaction: wallet_id=" + tx.WalletID.String())
		return fmt.Errorf("wallet not found: %s", tx.WalletID)
	}

	if ctx.OptionBool("queue") {
		ctx.Info("dispatching tx refresh to blockchain queue: chain=" + chain + " tx_hash=" + txHash + " wallet=" + wallet.ID.String() + " reason=" + reason)
		return facades.Queue().
			Job(&jobs.RefreshWalletTransactions{}, []queue.Arg{
				{Type: "string", Value: wallet.ID.String()},
				{Type: "string", Value: wallet.Chain},
			}).
			OnConnection("database").
			OnQueue("blockchain").
			Dispatch()
	}

	ctx.Info("sync mode: refreshing tx chain=" + chain + " tx_hash=" + txHash + " wallet=" + wallet.ID.String())
	if c.balances == nil {
		return fmt.Errorf("refresh:tx: balance refresh service is not initialized")
	}
	if err := c.balances.RefreshWallet(context.Background(), wallet); err != nil {
		ctx.Error("refresh failed: " + err.Error())
		return fmt.Errorf("refresh wallet for tx %s: %w", txHash, err)
	}
	ctx.Info("transaction " + txHash + " wallet refreshed successfully")
	return nil
}
