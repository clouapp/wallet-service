package providers

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/redis/go-redis/v9"

	blockstreamtip "github.com/macrowallets/waas/app/adapters/blockheight/blockstream"
	etherscantip "github.com/macrowallets/waas/app/adapters/blockheight/etherscan"
	mempooltip "github.com/macrowallets/waas/app/adapters/blockheight/mempool"
	solanatip "github.com/macrowallets/waas/app/adapters/blockheight/solana"

	"github.com/macrowallets/waas/app/adapters/redis/addresscache"
	"github.com/macrowallets/waas/app/adapters/redis/addressset"
	redispending "github.com/macrowallets/waas/app/adapters/redis/pending"
	"github.com/macrowallets/waas/app/adapters/redis/scanner"
	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/ingest"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// openChainEndpoint opens a sealed rpc_url and returns the URL to dial.
// A read failure does not include the URL.
func openChainEndpoint(stored string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("open chain rpc")
	}
	opened, err := settings.OpenStored(cipher, stored)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	endpoint, err := models.DialEndpoint(opened)
	if err != nil {
		return "", fmt.Errorf("open chain rpc")
	}
	return endpoint, nil
}

func registerVaultContainer(app foundation.Application) {
	app.Singleton(container.ContainerKey, func(app foundation.Application) (any, error) {
		c, err := buildVaultContainer(app)
		if err != nil {
			return nil, err
		}
		return c, nil
	})
	registerRuntimeServices(app)
}

func buildVaultContainer(app foundation.Application) (*container.Container, error) {
	c := &container.Container{}

	redisClient, err := facades.Redis()
	if err != nil {
		return nil, fmt.Errorf("vault: redis: %w", err)
	}
	c.Redis = redisClient

	secrets, err := resolve[*sweepsecrets.SDKClient](app)
	if err != nil {
		return nil, err
	}
	c.SecretsManager = secrets
	mpcService, err := resolve[*mpc.TSSService](app)
	if err != nil {
		return nil, err
	}
	c.MPCService = mpcService

	users, err := resolve[*repositories.UserRepository](app)
	if err != nil {
		return nil, err
	}
	refreshTokens, err := resolve[*repositories.RefreshTokenRepository](app)
	if err != nil {
		return nil, err
	}
	passwordResets, err := resolve[*repositories.PasswordResetTokenRepository](app)
	if err != nil {
		return nil, err
	}
	recoveryCodes, err := resolve[*repositories.TotpRecoveryCodeRepository](app)
	if err != nil {
		return nil, err
	}
	accounts, err := resolve[*repositories.AccountRepository](app)
	if err != nil {
		return nil, err
	}
	memberships, err := resolve[*repositories.AccountUserRepository](app)
	if err != nil {
		return nil, err
	}
	accessTokens, err := resolve[*repositories.AccessTokenRepository](app)
	if err != nil {
		return nil, err
	}
	c.UserRepo = users
	c.RefreshTokenRepo = refreshTokens
	c.PasswordResetTokenRepo = passwordResets
	c.TotpRecoveryCodeRepo = recoveryCodes
	c.AccountRepo = accounts
	c.AccountUserRepo = memberships
	c.AccessTokenRepo = accessTokens
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	walletUsers, err := resolve[*repositories.WalletUserRepository](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	whitelist, err := resolve[*repositories.WhitelistEntryRepository](app)
	if err != nil {
		return nil, err
	}
	assetBalances, err := resolve[*repositories.WalletAssetBalanceRepository](app)
	if err != nil {
		return nil, err
	}
	balanceSnapshots, err := resolve[*repositories.WalletBalanceSnapshotRepository](app)
	if err != nil {
		return nil, err
	}
	utxos, err := resolve[*repositories.WalletUTXORepository](app)
	if err != nil {
		return nil, err
	}
	syncStates, err := resolve[*repositories.WalletSyncStateRepository](app)
	if err != nil {
		return nil, err
	}
	c.WalletRepo = wallets
	c.WalletUserRepo = walletUsers
	c.AddressRepo = addresses
	transactions, err := resolve[*repositories.TransactionRepository](app)
	if err != nil {
		return nil, err
	}
	withdrawals, err := resolve[*repositories.WithdrawalRepository](app)
	if err != nil {
		return nil, err
	}
	c.TransactionRepo = transactions
	c.WithdrawalRepo = withdrawals
	chains, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return nil, err
	}
	tokens, err := resolve[*repositories.TokenRepository](app)
	if err != nil {
		return nil, err
	}
	chainResources, err := resolve[*repositories.ChainResourceRepository](app)
	if err != nil {
		return nil, err
	}
	currencies, err := resolve[*repositories.CurrencyRepository](app)
	if err != nil {
		return nil, err
	}
	webhookConfigs, err := resolve[*repositories.WebhookConfigRepository](app)
	if err != nil {
		return nil, err
	}
	webhookEvents, err := resolve[*repositories.WebhookEventRepository](app)
	if err != nil {
		return nil, err
	}
	c.WebhookConfigRepo = webhookConfigs
	c.WebhookEventRepo = webhookEvents
	c.WhitelistEntryRepo = whitelist
	c.ChainRepo = chains
	c.TokenRepo = tokens
	c.ChainResourceRepo = chainResources
	webhookSubscriptions, err := resolve[*repositories.WebhookSubscriptionRepository](app)
	if err != nil {
		return nil, err
	}
	c.WebhookSubscriptionRepo = webhookSubscriptions
	c.WalletAssetBalanceRepo = assetBalances
	c.WalletBalanceSnapshotRepo = balanceSnapshots
	c.WalletUTXORepo = utxos
	c.WalletSyncStateRepo = syncStates
	c.CurrencyRepo = currencies

	accountSettings, err := container.Make[*settings.Service]()
	if err != nil {
		return nil, fmt.Errorf("vault: account settings: %w", err)
	}
	webhookSync, err := resolve[*webhooksync.Service](app)
	if err != nil {
		return nil, err
	}
	c.WebhookSyncService = webhookSync

	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	c.Registry = registry
	registryService, err := resolve[*chainregistry.ChainRegistryService](app)
	if err != nil {
		return nil, fmt.Errorf("vault: chain registry: %w", err)
	}
	networkByChain := registryService.Networks()

	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	c.WebhookService = webhookService
	c.WalletService = wallet.NewService(wallet.Deps{
		Registry:     c.Registry,
		AddressCache: addresscache.New(c.Redis),
		MPC:          c.MPCService,
		Secrets:      sweepsecrets.NewWalletStore(c.SecretsManager),
		Wallets:      c.WalletRepo,
		Addresses:    c.AddressRepo,
		WebhookSync:  c.WebhookSyncService,
	})
	flags, err := container.Make[*features.Service]()
	if err != nil {
		return nil, fmt.Errorf("vault: feature flags: %w", err)
	}
	prices, err := resolve[*price.Service](app)
	if err != nil {
		return nil, err
	}
	c.PriceService = prices
	c.SweepService = sweep.NewService(sweep.Deps{
		Registry:     c.Registry,
		MPC:          c.MPCService,
		Secrets:      sweepsecrets.New(c.SecretsManager),
		Cache:        facades.Cache(),
		Webhook:      c.WebhookService,
		Wallets:      c.WalletRepo,
		Addresses:    c.AddressRepo,
		Transactions: c.TransactionRepo,
		SweepLimits:  accountSettings.EffectiveSweepLimits,
		Chains:       c.ChainRepo,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		},
		GasDefaults: nil,
		TokenPricer: c.PriceService,
	})
	c.WithdrawalService = withdraw.NewService(withdraw.Deps{
		Registry:     c.Registry,
		Webhook:      c.WebhookService,
		MPC:          c.MPCService,
		Cache:        facades.Cache(),
		Transactions: c.TransactionRepo,
		Wallets:      c.WalletRepo,
		Addresses:    c.AddressRepo,
		Sweep:        c.SweepService,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused)
		},
	})
	c.WithdrawalService.UseUSDQuote(c.PriceService)
	verifier, err := resolve[*authsvc.SecondFactorVerifier](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal create: %w", err)
	}
	withdrawalRows, err := resolve[*withdrawalrecords.Records](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal create: %w", err)
	}
	c.WithdrawalService.UseCreate(c.UserRepo, verifier, withdrawalRows, c.ChainRepo)

	etherscanKey := func(ctx context.Context) string {
		envKey := facades.Config().GetString("vault.webhooks.etherscan_api_key")
		return accountSettings.EtherscanKeyForHeight(ctx, envKey)
	}
	blockHeightProviders := blockheight.NewProviders(blockheight.ProvidersDeps{
		Key:            etherscanKey,
		NetworkByChain: networkByChain,
		Etherscan:      etherscantip.New(blockheight.EtherscanDeps{KeyAtUse: etherscanKey}),
		Blockstream:    blockstreamtip.New(),
		Testnet4:       mempooltip.New(),
		Solana:         solanatip.New(),
	})
	assetDecimals := withdrawalevents.NewRegistryDecimals(withdrawalevents.RegistryDecimalsDeps{
		Registry: c.Registry,
		Chains:   c.ChainRepo,
	})
	c.WithdrawalEvents = withdrawalevents.NewPublisher(withdrawalevents.PublisherDeps{
		Enqueuer:     c.WebhookService,
		Withdrawals:  c.WithdrawalRepo,
		Transactions: c.TransactionRepo,
		Wallets:      c.WalletRepo,
		Decimals:     assetDecimals,
	})
	c.DepositEvents = depositevents.NewPublisher(depositevents.PublisherDeps{
		Enqueuer: c.WebhookService,
		Wallets:  c.WalletRepo,
		Decimals: assetDecimals,
	})
	c.DepositService = deposit.NewService(deposit.Deps{
		Store:                scanner.New(c.Redis),
		Registry:             c.Registry,
		Webhook:              c.WebhookService,
		Addresses:            c.AddressRepo,
		Transactions:         c.TransactionRepo,
		BlockHeightProviders: blockHeightProviders,
	})
	c.DepositService.SetWithdrawalConfirmations(c.WithdrawalEvents)
	c.DepositService.SetDepositEvents(c.DepositEvents)
	// Each ScanLatestBlocks call reads deposit_scan and runs
	// deposit.ScanOptionsFromSettings. A stored value overrides DEPOSIT_SCAN_*.
	// A missing row, an invalid value, or a failed read keeps the environment
	// window and the scan continues.
	c.DepositService.SetScanOptionSource(func(ctx context.Context) (deposit.ScanOptions, error) {
		envBatch := facades.Config().GetInt("vault.deposit_scan.batch_blocks")
		envCatchUp := facades.Config().GetInt("vault.deposit_scan.catch_up_blocks")
		envConcurrency := facades.Config().GetInt("vault.deposit_scan.concurrency")
		stored, readErr := accountSettings.EffectiveDepositScan(ctx)
		return deposit.ScanOptionsForRun(
			envBatch, envCatchUp, envConcurrency,
			stored.BatchBlocks, stored.CatchUpBlocks, stored.Concurrency,
			readErr,
		)
	})
	failurePolicy, err := deposit.FailurePolicyFromSettings(
		facades.Config().GetInt("vault.deposit_scan.retry_attempts"),
		facades.Config().GetInt("vault.deposit_scan.retry_delay_ms"),
		facades.Config().GetInt("vault.deposit_scan.pending_retry_seconds"),
		facades.Config().GetInt("vault.deposit_scan.pending_retry_max_seconds"),
		facades.Config().GetInt("vault.deposit_scan.max_new_pending_per_cycle"),
	)
	if err != nil {
		return nil, fmt.Errorf("vault: deposit failure policy: %w", err)
	}
	if err := c.DepositService.SetFailurePolicy(failurePolicy); err != nil {
		return nil, fmt.Errorf("vault: deposit failure policy: %w", err)
	}
	if pendingDeposits := buildPendingDepositStore(c.Redis, facades.Config().GetString("vault.deposit_scan.pending_dir")); pendingDeposits != nil {
		c.DepositService.SetPendingStore(pendingDeposits)
	}
	c.IngestService = ingest.NewService(ingest.Deps{
		Addresses:    addressset.New(c.Redis),
		Registry:     c.Registry,
		Webhook:      c.WebhookService,
		AddressRepo:  c.AddressRepo,
		Transactions: c.TransactionRepo,
	})
	c.IngestService.SetDepositEvents(c.DepositEvents)
	c.BalanceRefreshService = refresh.NewBalanceService(refresh.Deps{
		Registry:      c.Registry,
		Wallets:       c.WalletRepo,
		AssetBalances: c.WalletAssetBalanceRepo,
		Snapshots:     c.WalletBalanceSnapshotRepo,
		SyncStates:    c.WalletSyncStateRepo,
	})
	walletRefresher, err := refresh.NewWalletRefresher(refresh.WalletRefresherDeps{
		Balances: c.BalanceRefreshService,
		Wallets:  c.WalletRepo,
		Chains:   c.Registry,
		Spacing:  time.Duration(facades.Config().GetInt("vault.local_workers.balance_refresh_spacing_ms")) * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("vault: wallet refresher: %w", err)
	}
	c.WalletRefresher = walletRefresher
	c.DepositService.SetBalanceRefresher(c.WalletRefresher)

	return c, nil
}

// buildPendingDepositStore keeps failed deposit blocks in Redis and in a local
// append-only file; either one is enough, and with neither the scanner stops before a
// failing block instead of skipping it.
func buildPendingDepositStore(rdb redis.UniversalClient, dir string) pending.Store {
	var redisStore pending.Store
	if rdb != nil {
		store, err := redispending.NewRedisStore(redispending.RedisStoreDeps{Redis: rdb, KeyPrefix: redispending.DefaultRedisKeyPrefix})
		if err != nil {
			slog.Error("vault: pending deposit redis store unavailable", "error", err)
		} else {
			redisStore = store
		}
	}
	if dir == "" {
		dir = defaultPendingDepositDir()
	}
	fileStore, err := pending.NewFileStore(pending.FileStoreDeps{Dir: dir})
	if err != nil {
		slog.Error("vault: pending deposit file store unavailable", "dir", dir, "error", err)
		fileStore = nil
	}
	store, err := pending.NewDurableStore(pending.DurableStoreDeps{Redis: redisStore, File: fileStore})
	if err != nil {
		slog.Error("vault: no pending deposit store; a block that keeps failing stops the scan", "error", err)
		return nil
	}
	slog.Info("vault: pending deposit store ready", "redis", redisStore != nil, "file_dir", dir, "file", fileStore != nil)
	return store
}

func defaultPendingDepositDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "macro-wallets", "deposit-pending")
	}
	return filepath.Join(os.TempDir(), "macro-wallets", "deposit-pending")
}

// lenientLogScanChains keep their deployed deposit scan: a block whose eth_getLogs
// fails is scanned for native transfers only. Every other EVM record fails the block
// so the scanner retries it (evmchain.EVMConfig.StrictLogScan).
var lenientLogScanChains = map[string]bool{
	models.ChainETH:      true,
	models.ChainTETH:     true,
	models.ChainPolygon:  true,
	models.ChainTPolygon: true,
}

// resolveGasReadinessThreshold is the chains.gas_readiness_threshold_raw value.
// An empty column means the chain has no gas threshold. Environment variables
// do not fill it in.
func resolveGasReadinessThreshold(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.GasReadinessThreshold()
}

// resolveDustThresholdNative is the chains.dust_threshold_native_raw value.
// An empty column means no native dust filter. Environment variables do not
// fill it in.
func resolveDustThresholdNative(ch *models.Chain) *big.Int {
	if ch == nil {
		return nil
	}
	return ch.DustThresholdNative()
}
