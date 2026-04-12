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

var ambiguousCurrencies = map[string]bool{
	"usdt": true,
	"usdc": true,
	"dai":  true,
	"wbtc": true,
	"weth": true,
}

type RefreshCurrency struct{}

func (c *RefreshCurrency) Signature() string {
	return "refresh:currency"
}

func (c *RefreshCurrency) Description() string {
	return "Refresh read model for all addresses holding a currency"
}

func (c *RefreshCurrency) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "currency",
				Usage:    "currency symbol (e.g. eth, btc, usdt)",
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
			&command.StringFlag{Name: "chain", Usage: "required when currency exists on multiple chains"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshCurrency) Handle(ctx console.Context) error {
	currency := strings.ToLower(ctx.ArgumentString("currency"))
	addresses := ctx.ArgumentStringSlice("addresses")
	chainFlag := ctx.Option("chain")
	reason := ctx.Option("reason")

	if len(addresses) == 0 {
		ctx.Error("at least one address is required")
		return fmt.Errorf("at least one address is required")
	}

	if ambiguousCurrencies[currency] && chainFlag == "" {
		ctx.Error(fmt.Sprintf("currency %q exists on multiple chains — provide --chain to disambiguate", currency))
		return fmt.Errorf("ambiguous currency %q requires --chain flag", currency)
	}

	ctr := container.Get()
	chain, err := resolveCurrencyToChain(ctr, currency, chainFlag)
	if err != nil {
		ctx.Error(err.Error())
		return err
	}

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
			ctx.Info("dispatching refresh for currency=" + currency + " address=" + addr + " wallet=" + wallet.ID.String() + " reason=" + reason)
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

		ctx.Info("sync mode: refreshing currency=" + currency + " address=" + addr + " wallet=" + wallet.ID.String())
		if err := ctr.BalanceRefreshService.RefreshWallet(context.Background(), wallet); err != nil {
			ctx.Error("refresh failed for address " + addr + ": " + err.Error())
			return fmt.Errorf("refresh address %s: %w", addr, err)
		}
		ctx.Info("address " + addr + " refreshed successfully")
	}

	ctx.Info("processed " + fmt.Sprint(len(addresses)) + " address(es) for currency=" + currency + " chain=" + chain)
	return nil
}

func resolveCurrencyToChain(ctr *container.Container, currency, chainFlag string) (string, error) {
	if chainFlag != "" {
		return chainFlag, nil
	}

	for _, id := range ctr.Registry.ChainIDs() {
		if id == currency {
			return id, nil
		}
	}

	for _, id := range ctr.Registry.ChainIDs() {
		adapter, err := ctr.Registry.Chain(id)
		if err != nil {
			continue
		}
		if strings.EqualFold(adapter.NativeAsset(), currency) {
			return id, nil
		}
	}

	return "", fmt.Errorf("cannot resolve currency %q to a chain — provide --chain", currency)
}
