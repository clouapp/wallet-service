package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type RefreshWallet struct {
	balances *refresh.BalanceService
}

// NewRefreshWallet refreshes one wallet's read model.
func NewRefreshWallet(balances *refresh.BalanceService) *RefreshWallet {
	if balances == nil {
		panic("refresh:wallet: balance refresh service is required")
	}
	return &RefreshWallet{balances: balances}
}

func (c *RefreshWallet) Signature() string {
	return "refresh:wallet"
}

func (c *RefreshWallet) Description() string {
	return "Refresh a wallet read model (sync-first by default)"
}

func (c *RefreshWallet) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "wallet_id",
				Usage:    "wallet UUID to refresh",
				Required: true,
			},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.StringFlag{Name: "chain", Usage: "chain override"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshWallet) Handle(ctx console.Context) error {
	walletID := ctx.ArgumentString("wallet_id")
	scope := ctx.Option("scope")
	reason := ctx.Option("reason")

	id, err := uuid.Parse(walletID)
	if err != nil {
		ctx.Error("invalid wallet_id: " + walletID)
		return fmt.Errorf("invalid wallet_id: %w", err)
	}

	wallet, err := container.MustMake[*walletrecords.Wallets]().FindByID(context.Background(), id)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		ctx.Error("failed to load wallet: " + err.Error())
		return fmt.Errorf("load wallet: %w", err)
	}
	if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		ctx.Error("wallet not found: " + walletID)
		return fmt.Errorf("wallet not found: %s", walletID)
	}

	chainID := ctx.Option("chain")
	if chainID == "" {
		chainID = wallet.Chain
	}

	if ctx.OptionBool("queue") {
		ctx.Info("dispatching wallet refresh to blockchain queue: wallet=" + walletID + " scope=" + scope + " reason=" + reason)
		return dispatchScopedJobs(scope, walletID, chainID)
	}

	ctx.Info("sync mode: refreshing wallet=" + walletID + " scope=" + scope + " reason=" + reason)
	if c.balances == nil {
		return fmt.Errorf("refresh:wallet: balance refresh service is not initialized")
	}
	if err := c.balances.RefreshWallet(context.Background(), wallet); err != nil {
		ctx.Error("refresh failed: " + err.Error())
		return fmt.Errorf("refresh wallet: %w", err)
	}
	ctx.Info("wallet " + walletID + " refreshed successfully")
	return nil
}

func dispatchScopedJobs(scope, walletID, chainID string) error {
	args := []queue.Arg{
		{Type: "string", Value: walletID},
		{Type: "string", Value: chainID},
	}
	dispatch := func(job queue.Job) error {
		return facades.Queue().
			Job(job, args).
			OnConnection("database").
			OnQueue("blockchain").
			Dispatch()
	}

	switch scope {
	case "balances":
		return dispatch(&jobs.RefreshWalletBalances{})
	case "transactions":
		return dispatch(&jobs.RefreshWalletTransactions{})
	case "tokens":
		return dispatch(&jobs.RefreshWalletTokens{})
	case "utxos":
		return dispatch(&jobs.RefreshWalletUTXOs{})
	case "full":
		for _, job := range []queue.Job{
			&jobs.RefreshWalletBalances{},
			&jobs.RefreshWalletTransactions{},
			&jobs.RefreshWalletTokens{},
			&jobs.RefreshWalletUTXOs{},
		} {
			if err := dispatch(job); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}
