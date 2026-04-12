package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/jobs"
)

type RefreshAddress struct{}

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

	ctr := container.Get()
	useQueue := ctx.OptionBool("queue")

	for _, addr := range addresses {
		addrRecord, err := ctr.AddressRepo.FindByChainAndAddress(chain, addr)
		if err != nil {
			ctx.Error("failed to look up address " + addr + ": " + err.Error())
			return fmt.Errorf("look up address %s: %w", addr, err)
		}
		if addrRecord == nil {
			ctx.Error("address not found: chain=" + chain + " address=" + addr)
			continue
		}

		wallet, err := ctr.WalletRepo.FindByID(addrRecord.WalletID)
		if err != nil {
			ctx.Error("failed to load wallet for address " + addr + ": " + err.Error())
			return fmt.Errorf("load wallet for address %s: %w", addr, err)
		}
		if wallet == nil {
			ctx.Error("wallet not found for address " + addr)
			continue
		}

		if useQueue {
			ctx.Info("dispatching refresh for address " + addr + " wallet=" + wallet.ID.String() + " reason=" + reason)
			if err := facades.Queue().
				Job(&jobs.RefreshWalletBalances{}, []queue.Arg{
					{Type: "string", Value: wallet.ID.String()},
					{Type: "string", Value: wallet.Chain},
				}).
				OnConnection("database").
				OnQueue("blockchain").
				Dispatch(); err != nil {
				ctx.Error("dispatch failed for address " + addr + ": " + err.Error())
				return fmt.Errorf("dispatch for address %s: %w", addr, err)
			}
			continue
		}

		ctx.Info("sync mode: refreshing address " + addr + " wallet=" + wallet.ID.String())
		if err := ctr.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
			ctx.Error("refresh failed for address " + addr + ": " + err.Error())
			return fmt.Errorf("refresh address %s: %w", addr, err)
		}
		ctx.Info("address " + addr + " refreshed successfully")
	}

	ctx.Info("processed " + fmt.Sprint(len(addresses)) + " address(es) on chain=" + chain + " [" + strings.Join(addresses, ", ") + "]")
	return nil
}
