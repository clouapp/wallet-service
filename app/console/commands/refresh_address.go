package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

type RefreshAddress struct {
	balances   *refresh.BalanceService
	dispatcher refresh.Dispatcher
}

// RefreshAddressDeps is everything the refresh:address command needs.
// Balances and Dispatcher are required.
type RefreshAddressDeps struct {
	Balances   *refresh.BalanceService
	Dispatcher refresh.Dispatcher
}

// NewRefreshAddress refreshes the wallets that own the given addresses.
func NewRefreshAddress(deps RefreshAddressDeps) *RefreshAddress {
	if deps.Balances == nil {
		panic("refresh:address: balance refresh service is required")
	}
	if deps.Dispatcher == nil {
		panic("refresh:address: refresh dispatcher is required")
	}
	return &RefreshAddress{balances: deps.Balances, dispatcher: deps.Dispatcher}
}

func (c *RefreshAddress) Signature() string {
	return "refresh:address"
}

func (c *RefreshAddress) Description() string {
	return "Refresh read model for one or more addresses on a chain"
}

func (c *RefreshAddress) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "chain",
				Usage:    "blockchain identifier (e.g. ethereum, bitcoin, solana)",
				Required: true,
			},
			&command.ArgumentStringSlice{
				Name:  "addresses",
				Usage: "one or more addresses to refresh",
				Min:   1,
				Max:   -1,
			},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshAddress) Handle(ctx console.Context) error {
	chain := ctx.ArgumentString("chain")
	addresses := ctx.ArgumentStringSlice("addresses")
	reason := ctx.Option("reason")

	if len(addresses) == 0 {
		ctx.Error("at least one address is required")
		return fmt.Errorf("at least one address is required")
	}

	useQueue := ctx.OptionBool("queue")

	for _, addr := range addresses {
		addrRecord, err := container.MustMake[*walletrecords.Addresses]().FindByChainAndAddress(context.Background(), chain, addr)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			ctx.Error("failed to look up address " + addr + ": " + err.Error())
			return fmt.Errorf("look up address %s: %w", addr, err)
		}
		if err != nil || addrRecord == nil {
			ctx.Error("address not found: chain=" + chain + " address=" + addr)
			continue
		}

		wallet, err := container.MustMake[*walletrecords.Wallets]().FindByID(context.Background(), addrRecord.WalletID)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			ctx.Error("failed to load wallet for address " + addr + ": " + err.Error())
			return fmt.Errorf("load wallet for address %s: %w", addr, err)
		}
		if wallet == nil || errors.Is(err, models.ErrRepositoryNotFound) {
			ctx.Error("wallet not found for address " + addr)
			continue
		}

		if useQueue {
			ctx.Info("dispatching refresh for address " + addr + " wallet=" + wallet.ID.String() + " reason=" + reason)
			if c.dispatcher == nil {
				return fmt.Errorf("refresh:address: refresh dispatcher is not initialized")
			}
			if err := c.dispatcher.DispatchBalances(wallet.ID.String(), wallet.Chain); err != nil {
				ctx.Error("dispatch failed for address " + addr + ": " + err.Error())
				return fmt.Errorf("dispatch for address %s: %w", addr, err)
			}
			continue
		}

		ctx.Info("sync mode: refreshing address " + addr + " wallet=" + wallet.ID.String())
		if c.balances == nil {
			return fmt.Errorf("refresh:address: balance refresh service is not initialized")
		}
		if err := c.balances.RefreshWallet(context.Background(), wallet); err != nil {
			ctx.Error(redactedLine("refresh failed for address " + addr + ": " + err.Error()))
			return fmt.Errorf("refresh address %s: %w", addr, redactedError(err))
		}
		ctx.Info("address " + addr + " refreshed successfully")
	}

	ctx.Info("processed " + fmt.Sprint(len(addresses)) + " address(es) on chain=" + chain + " [" + strings.Join(addresses, ", ") + "]")
	return nil
}
