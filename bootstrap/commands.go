package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"

	secretsadapter "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/repositories/chaincatalog"
	"github.com/macrowallets/waas/app/services/activity"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/keyexport"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// Commands are the artisan commands of the application, each built with the
// services it runs. `artisan make:command` appends to the returned literal.
// Every command exits 1 when it fails (exitOnFailures).
func Commands() []console.Command {
	balances := container.MustMake[*refresh.BalanceService]()
	deposits := container.MustMake[*deposit.Service]()
	registry := container.MustMake[*chainpkg.Registry]()
	prices := container.MustMake[*price.Service]()
	wallets := container.MustMake[*walletrecords.Wallets]()
	addresses := container.MustMake[*walletrecords.Addresses]()
	transactions := container.MustMake[*walletrecords.Transactions]()
	return exitOnFailures([]console.Command{
		commands.NewRefreshWallet(commands.RefreshWalletDeps{
			Balances: balances,
			Wallets:  wallets,
		}),
		commands.NewRefreshAddress(commands.RefreshAddressDeps{
			Balances:  balances,
			Wallets:   wallets,
			Addresses: addresses,
		}),
		commands.NewRefreshCurrency(commands.RefreshCurrencyDeps{
			Registry:  registry,
			Balances:  balances,
			Wallets:   wallets,
			Addresses: addresses,
		}),
		commands.NewRefreshTx(commands.RefreshTxDeps{
			Balances:     balances,
			Wallets:      wallets,
			Transactions: transactions,
		}),
		commands.NewScanDeposits(deposits),
		commands.NewReconcileWallet(commands.ReconcileWalletDeps{
			Balances: balances,
			Wallets:  wallets,
		}),
		commands.NewPriceWebSocket(commands.PriceWebSocketDeps{
			Prices:     prices,
			CoinAPIKey: container.MustMake[*price.CoinAPICredential]().Key,
			Cache:      appfacades.Cache(),
		}),
		commands.NewPriceCheckUpdate(prices),
		commands.NewChainsSetRPC(chains.NewReplaceRPC(chains.ReplaceRPCDeps{
			Store: container.MustMake[*repositories.ChainRepository](),
			Seal:  func(plaintext string) (string, error) { return settings.Seal(appfacades.Crypt(), plaintext) },
		})),
		commands.NewChainsAlignNetwork(chainregistry.NewAligner(chainregistry.AlignerDeps{
			Store:   repositories.NewChainRegistryRepository(nil),
			Decrypt: decryptChainRPC,
			Probe:   chainregistry.ProbeRPCNetwork,
			Cache:   deposits,
			Profile: configuredChainProfile,
		})),
		commands.NewChainsAddMissing(chainregistry.NewMissingChains(seedMissingAddedChains)),
		commands.NewWithdrawPreflight(commands.WithdrawPreflightDeps{
			Sweep:       container.MustMake[*sweep.Box]().Service,
			Chains:      registry,
			WalletChain: walletChainLookup(wallets),
		}),
		commands.NewPruneActivity(container.MustMake[*activity.Service]()),
		commands.NewEVMCall(commands.EVMCallDeps{
			Wallets: container.MustMake[*repositories.WalletRepository](),
			Signer:  evmCallSigner(),
		}),
		commands.NewWalletsExportKeys(commands.WalletsExportKeysDeps{
			Wallets:    container.MustMake[*repositories.WalletRepository](),
			Addresses:  container.MustMake[*repositories.AddressRepository](),
			Chains:     container.MustMake[*repositories.ChainRepository](),
			ShareB:     exportShareB,
			DecryptRPC: decryptChainRPC,
			AppEnv:     appfacades.Config().GetString("app.env"),
		}),
		commands.NewTransactionsBackfillFees(deposits, registry),
	})
}

const chainNetworkProfileConfigKey = "vault.chains.network_profile"

func seedMissingAddedChains(ctx context.Context, apply bool) (chainregistry.AddedChainsPlan, error) {
	plan, err := chaincatalog.New(appfacades.Config(), appfacades.Crypt()).SeedMissingAddedChains(ctx, apply)
	if err != nil {
		return chainregistry.AddedChainsPlan{}, err
	}
	chains := make([]chainregistry.AddedChain, len(plan.Chains))
	for i, added := range plan.Chains {
		chains[i] = chainregistry.AddedChain{
			ID: added.ID, AdapterType: added.AdapterType, EnvVar: added.EnvVar, Network: added.Network,
			NetworkID: added.NetworkID, IsTestnet: added.IsTestnet,
		}
	}
	return chainregistry.AddedChainsPlan{
		Chains: chains, Tokens: plan.Tokens, Resources: plan.Resources, Skipped: plan.Skipped,
	}, nil
}

func decryptChainRPC(encrypted string) (string, error) {
	stored, err := settings.OpenStored(appfacades.Crypt(), encrypted)
	if err != nil {
		return "", err
	}
	return models.ResolveRPCURL(stored)
}

// walletChainLookup answers which chain a wallet is on for withdraw:preflight.
func walletChainLookup(wallets *walletrecords.Wallets) func(ctx context.Context, walletID uuid.UUID) (string, error) {
	return func(ctx context.Context, walletID uuid.UUID) (string, error) {
		wallet, err := wallets.FindByID(ctx, walletID)
		if err != nil || wallet == nil {
			return "", fmt.Errorf("wallet %s not found", walletID)
		}
		return wallet.Chain, nil
	}
}

// exportShareB opens the Secrets Manager reader when wallets:export-keys runs,
// so a process without Secrets Manager still serves the other commands.
func exportShareB() (keyexport.ShareBSource, error) {
	secrets, err := container.Make[*secretsmanager.Client]()
	if err != nil {
		return nil, errors.New("secrets manager is not configured; share B cannot be fetched")
	}
	return secretsadapter.NewShareB(secrets), nil
}

func configuredChainProfile() string {
	return strings.TrimSpace(appfacades.Config().GetString(chainNetworkProfileConfigKey))
}

func evmCallSigner() evmcall.Signer {
	box := container.MustMake[*sweep.Box]()
	if box == nil || box.Service == nil {
		panic(fmt.Errorf("sweep service cannot sign evm calls"))
	}
	signer, ok := box.Service.(evmcall.Signer)
	if !ok || signer == nil {
		panic(fmt.Errorf("sweep service cannot sign evm calls"))
	}
	return signer
}
