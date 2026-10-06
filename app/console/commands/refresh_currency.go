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
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/pkg/types"
)

var ambiguousCurrencies = map[string]bool{
	"usdt": true,
	"usdc": true,
	"dai":  true,
	"wbtc": true,
	"weth": true,
}

type RefreshCurrency struct {
	registry   *chainpkg.Registry
	balances   *refresh.BalanceService
	dispatcher refresh.Dispatcher
}

// RefreshCurrencyDeps is everything the refresh:currency command needs.
// Registry, Balances, and Dispatcher are required.
type RefreshCurrencyDeps struct {
	Registry   *chainpkg.Registry
	Balances   *refresh.BalanceService
	Dispatcher refresh.Dispatcher
}

// NewRefreshCurrency refreshes wallets that hold one currency.
func NewRefreshCurrency(deps RefreshCurrencyDeps) *RefreshCurrency {
	if deps.Registry == nil {
		panic("refresh:currency: chain registry is required")
	}
	if deps.Balances == nil {
		panic("refresh:currency: balance refresh service is required")
	}
	if deps.Dispatcher == nil {
		panic("refresh:currency: refresh dispatcher is required")
	}
	return &RefreshCurrency{registry: deps.Registry, balances: deps.Balances, dispatcher: deps.Dispatcher}
}

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

	chain, err := resolveCurrencyToChain(c.registry, currency, chainFlag)
	if err != nil {
		ctx.Error(err.Error())
		return err
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
			ctx.Info("dispatching refresh for currency=" + currency + " address=" + addr + " wallet=" + wallet.ID.String() + " reason=" + reason)
			if c.dispatcher == nil {
				return fmt.Errorf("refresh:currency: refresh dispatcher is not initialized")
			}
			if err := c.dispatcher.DispatchBalances(wallet.ID.String(), wallet.Chain); err != nil {
				ctx.Error("dispatch failed for address " + addr + ": " + err.Error())
				return fmt.Errorf("dispatch for address %s: %w", addr, err)
			}
			continue
		}

		ctx.Info("sync mode: refreshing currency=" + currency + " address=" + addr + " wallet=" + wallet.ID.String())
		if c.balances == nil {
			return fmt.Errorf("refresh:currency: balance refresh service is not initialized")
		}
		if err := c.balances.RefreshWallet(context.Background(), wallet); err != nil {
			ctx.Error(redactedLine("refresh failed for address " + addr + ": " + err.Error()))
			return fmt.Errorf("refresh address %s: %w", addr, redactedError(err))
		}
		ctx.Info("address " + addr + " refreshed successfully")
	}

	ctx.Info("processed " + fmt.Sprint(len(addresses)) + " address(es) for currency=" + currency + " chain=" + chain)
	return nil
}

func resolveCurrencyToChain(registry *chainpkg.Registry, currency, chainFlag string) (string, error) {
	if registry == nil {
		return "", fmt.Errorf("refresh:currency: chain registry is not initialized")
	}
	if chainFlag != "" {
		return chainFlag, nil
	}

	for _, id := range registry.ChainIDs() {
		if id == currency {
			return id, nil
		}
	}

	for _, id := range registry.ChainIDs() {
		adapter, err := registry.Chain(id)
		if err != nil {
			continue
		}
		if types.SameAssetSymbol(adapter.NativeAsset(), currency) {
			return id, nil
		}
	}

	return "", fmt.Errorf("cannot resolve currency %q to a chain — provide --chain", currency)
}
