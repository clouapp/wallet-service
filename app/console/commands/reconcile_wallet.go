package commands

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/jobs"
)

type ReconcileWallet struct{}

func (c *ReconcileWallet) Signature() string {
	return "reconcile:wallet"
}

func (c *ReconcileWallet) Description() string {
	return "Reconcile a wallet by comparing on-chain state with the read model"
}

func (c *ReconcileWallet) Extend() command.Extend {
	return command.Extend{
		Category: "reconcile",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "wallet_id",
				Usage:    "wallet UUID to reconcile",
				Required: true,
			},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for reconciliation"},
		},
	}
}

func (c *ReconcileWallet) Handle(ctx console.Context) error {
	walletID := ctx.ArgumentString("wallet_id")
	reason := ctx.Option("reason")

	id, err := uuid.Parse(walletID)
	if err != nil {
		ctx.Error("invalid wallet_id: " + walletID)
		return fmt.Errorf("invalid wallet_id: %w", err)
	}

	ctr := container.Get()
	wallet, err := ctr.WalletRepo.FindByID(id)
	if err != nil {
		ctx.Error("failed to load wallet: " + err.Error())
		return fmt.Errorf("load wallet: %w", err)
	}
	if wallet == nil {
		ctx.Error("wallet not found: " + walletID)
		return fmt.Errorf("wallet not found: %s", walletID)
	}

	if ctx.OptionBool("queue") {
		ctx.Info("dispatching wallet reconciliation to blockchain queue: wallet=" + walletID + " reason=" + reason)
		return facades.Queue().
			Job(&jobs.ReconcileWalletState{}, []queue.Arg{
				{Type: "string", Value: walletID},
				{Type: "string", Value: wallet.Chain},
			}).
			OnConnection("database").
			OnQueue("blockchain").
			Dispatch()
	}

	ctx.Info("sync mode: reconciling wallet=" + walletID + " reason=" + reason)
	if err := ctr.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
		ctx.Error("reconciliation failed: " + err.Error())
		return fmt.Errorf("reconcile wallet: %w", err)
	}
	ctx.Info("wallet " + walletID + " reconciled successfully")
	return nil
}
