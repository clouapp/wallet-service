package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/dtos"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type RefreshWallet struct {
	balances       *refresh.BalanceService
	dispatcher     refresh.Dispatcher
	requestRefresh func(walletID, chainID string) error
}

// RefreshWalletDeps is everything the refresh:wallet command needs.
// Balances and Dispatcher are required.
type RefreshWalletDeps struct {
	Balances   *refresh.BalanceService
	Dispatcher refresh.Dispatcher
}

// NewRefreshWallet refreshes one wallet's read model.
func NewRefreshWallet(deps RefreshWalletDeps) *RefreshWallet {
	if deps.Balances == nil {
		panic("refresh:wallet: balance refresh service is required")
	}
	if deps.Dispatcher == nil {
		panic("refresh:wallet: refresh dispatcher is required")
	}
	return &RefreshWallet{
		balances:       deps.Balances,
		dispatcher:     deps.Dispatcher,
		requestRefresh: dispatchWalletRefreshRequested,
	}
}

// dispatchWalletRefreshRequested fires the registered WalletRefreshRequested
// event. Its listener enqueues the balance refresh.
func dispatchWalletRefreshRequested(walletID, chainID string) error {
	ev := facades.Event()
	if ev == nil {
		return fmt.Errorf("refresh:wallet: event dispatcher is not initialized")
	}
	return ev.Job(&dtos.WalletRefreshRequested{}, []event.Arg{
		{Type: "string", Value: walletID},
		{Type: "string", Value: chainID},
	}).Dispatch()
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
		return c.dispatchQueuedRefresh(scope, walletID, chainID)
	}

	ctx.Info("sync mode: refreshing wallet=" + walletID + " scope=" + scope + " reason=" + reason)
	if c.balances == nil {
		return fmt.Errorf("refresh:wallet: balance refresh service is not initialized")
	}
	if err := c.balances.RefreshWallet(context.Background(), wallet); err != nil {
		ctx.Error(redactedLine("refresh failed: " + err.Error()))
		return fmt.Errorf("refresh wallet: %w", redactedError(err))
	}
	ctx.Info("wallet " + walletID + " refreshed successfully")
	return nil
}

// dispatchQueuedRefresh sends a queued refresh through the registered event
// when the scope includes balances. The listener enqueues that job, so this
// path does not enqueue balances a second time.
func (c *RefreshWallet) dispatchQueuedRefresh(scope, walletID, chainID string) error {
	switch scope {
	case "balances":
		return c.requestWalletRefresh(walletID, chainID)
	case "full":
		if err := c.requestWalletRefresh(walletID, chainID); err != nil {
			return err
		}
		if c.dispatcher == nil {
			return fmt.Errorf("refresh:wallet: refresh dispatcher is not initialized")
		}
		for _, dispatch := range []func(string, string) error{
			c.dispatcher.DispatchTransactions,
			c.dispatcher.DispatchTokens,
			c.dispatcher.DispatchUTXOs,
		} {
			if err := dispatch(walletID, chainID); err != nil {
				return err
			}
		}
		return nil
	default:
		return c.dispatchScopedJobs(scope, walletID, chainID)
	}
}

func (c *RefreshWallet) requestWalletRefresh(walletID, chainID string) error {
	if c.requestRefresh == nil {
		return fmt.Errorf("refresh:wallet: event dispatcher is not initialized")
	}
	return c.requestRefresh(walletID, chainID)
}

func (c *RefreshWallet) dispatchScopedJobs(scope, walletID, chainID string) error {
	if c.dispatcher == nil {
		return fmt.Errorf("refresh:wallet: refresh dispatcher is not initialized")
	}
	switch scope {
	case "balances":
		return c.dispatcher.DispatchBalances(walletID, chainID)
	case "transactions":
		return c.dispatcher.DispatchTransactions(walletID, chainID)
	case "tokens":
		return c.dispatcher.DispatchTokens(walletID, chainID)
	case "utxos":
		return c.dispatcher.DispatchUTXOs(walletID, chainID)
	case "full":
		for _, dispatch := range []func(string, string) error{
			c.dispatcher.DispatchBalances,
			c.dispatcher.DispatchTransactions,
			c.dispatcher.DispatchTokens,
			c.dispatcher.DispatchUTXOs,
		} {
			if err := dispatch(walletID, chainID); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}
