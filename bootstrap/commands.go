package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
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
	"github.com/macrowallets/waas/app/services/balanceaudit"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/evmcall"
	"github.com/macrowallets/waas/app/services/ingestsubscription"
	"github.com/macrowallets/waas/app/services/keyexport"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/rpcendpoints"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sharemigration"
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
			Store:     container.MustMake[*repositories.ChainRepository](),
			Seal:      func(plaintext string) (string, error) { return settings.Seal(appfacades.Crypt(), plaintext) },
			Verify:    chainregistry.NewPrimaryVerifier(decryptChainRPC, configuredChainProfile),
			Fallbacks: container.MustMake[*repositories.ChainRPCEndpointRepository](),
		})),
		commands.NewChainsAlignNetwork(chainregistry.NewAligner(chainregistry.AlignerDeps{
			Store:       repositories.NewChainRegistryRepository(nil),
			Decrypt:     decryptChainRPC,
			Probe:       chainregistry.ProbeRPCNetwork,
			Cache:       deposits,
			Checkpoints: deposits,
			Fallbacks:   container.MustMake[*repositories.ChainRPCEndpointRepository](),
			Profile:     configuredChainProfile,
		})),
		commands.NewChainsRPCEndpoints(container.MustMake[*rpcendpoints.Service]()),
		commands.NewChainsAddMissing(chainregistry.NewMissingChains(seedMissingAddedChains)),
		commands.NewChainsAlignTokens(chainregistry.NewNetworkTokens(alignNetworkTokens)),
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
		commands.NewSecretsMigrateShares(sharemigration.NewService(sharemigration.Deps{
			Wallets:        container.MustMake[*repositories.WalletRepository](),
			Sources:        shareSourceAt,
			Target:         appShareStore(),
			TargetEndpoint: appfacades.Config().GetString("vault.aws.endpoint_url"),
			TargetKMSKeyID: secretsadapter.KMSKeyID(appfacades.Config().GetString("vault.master_key_ref")),
		})),
		commands.NewTransactionsBackfillFees(deposits, registry),
		commands.NewDepositsVerifyReceipts(deposits),
		// Read-only and not scheduled: run it daily from the host's scheduler if wanted.
		commands.NewDepositsAuditBalances(balanceaudit.NewService(balanceaudit.Deps{
			Chains:    registry,
			Addresses: container.MustMake[*repositories.AddressRepository](),
			Ledger:    container.MustMake[*repositories.TransactionRepository](),
		})),
		commands.NewWithdrawalsReconcile(deposits),
		commands.NewIngestSubscribe(container.MustMake[*ingestsubscription.Service]()),
		commands.NewIngestSubscriptions(container.MustMake[*ingestsubscription.Service]()),
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

func alignNetworkTokens(ctx context.Context, apply bool) (chainregistry.TokenAlignmentPlan, error) {
	alignment, err := chaincatalog.New(appfacades.Config(), appfacades.Crypt()).AlignNetworkTokens(ctx, apply)
	if err != nil {
		return chainregistry.TokenAlignmentPlan{}, err
	}
	changes := make([]chainregistry.TokenChange, len(alignment.Changes))
	for i, change := range alignment.Changes {
		changes[i] = chainregistry.TokenChange{
			ChainID: change.ChainID, Network: change.Network, Symbol: change.Symbol, Contract: change.Contract, Action: change.Action,
		}
	}
	return chainregistry.TokenAlignmentPlan{
		Changes: changes, Unknown: alignment.Unknown, Mainnet: alignment.Mainnet, Missing: alignment.Missing,
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

// appShareStore is the Secrets Manager the app itself uses, sealing new shares with
// the master key: the target of secrets:migrate-shares.
func appShareStore() *secretsadapter.WalletStore {
	secrets, err := container.Make[*secretsmanager.Client]()
	if err != nil {
		return secretsadapter.NewShareStore(nil, "")
	}
	return secretsadapter.NewShareStore(secrets, secretsadapter.KMSKeyID(appfacades.Config().GetString("vault.master_key_ref")))
}

// shareSourceAt is the Secrets Manager at endpoint (empty: AWS itself) read with the
// credentials of profile (empty: the app's own): the source of secrets:migrate-shares.
// It never writes.
func shareSourceAt(endpoint, profile string) (sharemigration.Source, error) {
	if profile == "" {
		cfg, err := container.Make[*aws.Config]()
		if err != nil {
			return nil, fmt.Errorf("aws config: %w", err)
		}
		return secretsadapter.NewShareStore(secretsadapter.NewClient(*cfg, endpoint), ""), nil
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(appfacades.Config().GetString("vault.aws.region")),
		awsconfig.WithSharedConfigProfile(profile),
	)
	if err != nil {
		return nil, fmt.Errorf("aws profile %s: %w", profile, err)
	}
	return secretsadapter.NewShareStore(secretsadapter.NewClient(cfg, endpoint), ""), nil
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
