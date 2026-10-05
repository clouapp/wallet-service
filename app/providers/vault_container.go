package providers

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/redis/go-redis/v9"

	blockstreamtip "github.com/macrowallets/waas/app/adapters/blockheight/blockstream"
	mempooltip "github.com/macrowallets/waas/app/adapters/blockheight/mempool"
	coinapiws "github.com/macrowallets/waas/app/adapters/price/coinapi"
	queuesqs "github.com/macrowallets/waas/app/adapters/queue/sqs"
	"github.com/macrowallets/waas/app/adapters/redis/addresscache"
	"github.com/macrowallets/waas/app/adapters/redis/addressset"
	redislock "github.com/macrowallets/waas/app/adapters/redis/lock"
	redispending "github.com/macrowallets/waas/app/adapters/redis/pending"
	"github.com/macrowallets/waas/app/adapters/redis/pricecache"
	"github.com/macrowallets/waas/app/adapters/redis/scanner"
	sweepredis "github.com/macrowallets/waas/app/adapters/redis/sweep"
	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
	"github.com/macrowallets/waas/pkg/security"
	"github.com/macrowallets/waas/pkg/types"
)

type staticEndpointResolver struct{ url string }

func (r staticEndpointResolver) ResolveEndpoint(
	ctx context.Context,
	params secretsmanager.EndpointParameters,
) (smithyendpoints.Endpoint, error) {
	u, err := url.Parse(r.url)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{URI: *u}, nil
}

// openChainEndpoint opens a sealed rpc_url and returns the URL to dial.
// A read failure does not include the URL.
func openChainEndpoint(stored string) (string, error) {
	cipher := facades.Crypt()
	if cipher == nil {
		return "", fmt.Errorf("open chain rpc")
	}
	opened, err := security.OpenSecret(cipher, stored)
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

	redisURL := facades.Config().GetString("vault.redis_url")
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			slog.Warn("vault: redis url parse failed", "error", err)
		} else {
			c.Redis = redis.NewClient(opts)
		}
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("vault: aws config: %w", err)
	}
	sqsClient := sqs.NewFromConfig(awsCfg)
	c.SQS = queue.NewSQSClient(queue.SQSClientDeps{
		Transport: queuesqs.New(sqsClient),
		URLs: queue.QueueURLs{
			Webhook: facades.Config().GetString("vault.queues.webhook"),
		},
	})

	smClient := secretsmanager.NewFromConfig(awsCfg)
	if endpoint := facades.Config().GetString("vault.aws.endpoint_url"); endpoint != "" {
		smClient = secretsmanager.NewFromConfig(awsCfg,
			secretsmanager.WithEndpointResolverV2(staticEndpointResolver{url: endpoint}))
	}
	c.SecretsManager = smClient
	c.MPCService = mpc.NewTSSService()

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

	if err := wireAuthServices(c); err != nil {
		return nil, err
	}

	c.PriceConfig.CoinGeckoAPIKey = facades.Config().GetString("vault.price.coingecko_api_key")
	c.PriceConfig.CoinMarketCapAPIKey = facades.Config().GetString("vault.price.coinmarketcap_api_key")
	c.PriceConfig.CoinAPIKey = facades.Config().GetString("vault.price.coinapi_key")

	accountSettings, err := container.Make[*settings.Service]()
	if err != nil {
		return nil, fmt.Errorf("vault: account settings: %w", err)
	}
	buildWebhookIngest(c, accountSettings)

	c.Registry = chainpkg.NewRegistry()

	tokensByChain := make(map[string][]types.Token)
	activeTokens, tokenErr := c.TokenRepo.FindActive(context.Background())
	if tokenErr != nil {
		slog.Error("failed to load tokens from DB", "error", tokenErr)
	} else {
		for _, t := range activeTokens {
			tok := types.Token{
				Symbol:   t.Symbol,
				Name:     t.Name,
				Contract: t.ContractAddress,
				Decimals: uint8(t.Decimals),
				ChainID:  t.ChainID,
			}
			tokensByChain[t.ChainID] = append(tokensByChain[t.ChainID], tok)
			c.Registry.RegisterToken(tok)
		}
	}

	networkByChain := make(map[string]string)
	activeChains, chainErr := c.ChainRepo.FindActive(context.Background())
	if chainErr != nil {
		slog.Error("failed to load chains from DB", "error", chainErr)
	} else {
		for _, ch := range activeChains {
			rpcURL, openErr := openChainEndpoint(ch.RpcURL)
			if openErr != nil {
				slog.Warn("failed to open chain rpc, skipping chain", "chain", ch.ID)
				continue
			}
			networkByChain[ch.ID] = ch.ResolveNetwork(rpcURL).Name
			var adapter types.Chain
			switch ch.AdapterType {
			case models.AdapterTypeEVM:
				networkID := int64(0)
				if ch.NetworkID != nil {
					networkID = *ch.NetworkID
				}
				adapter = chainpkg.NewEVMLive(chainpkg.EVMConfig{
					ChainIDStr:            ch.ID,
					ChainName:             ch.Name,
					NativeSymbol:          ch.NativeSymbol,
					NativeDecimal:         uint8(ch.NativeDecimals),
					NetworkID:             networkID,
					RPCURL:                rpcURL,
					Confirmations:         uint64(ch.RequiredConfirmations),
					ERC20Tokens:           tokensByChain[ch.ID],
					GasReadinessThreshold: resolveGasReadinessThreshold(&ch),
					DustThresholdNative:   resolveDustThresholdNative(&ch),
					StrictLogScan:         !lenientLogScanChains[ch.ID],
				})
			case models.AdapterTypeBitcoin:
				network := "mainnet"
				if ch.IsTestnet {
					network = "testnet"
				}
				adapter = chainpkg.NewBitcoinLive(chainpkg.BitcoinConfig{
					ChainIDStr:    ch.ID,
					ChainName:     ch.Name,
					NativeSymbol:  ch.NativeSymbol,
					RPCURL:        rpcURL,
					Network:       network,
					IsTestnet:     ch.IsTestnet,
					Confirmations: uint64(ch.RequiredConfirmations),
				})
			case models.AdapterTypeSolana:
				adapter = chainpkg.NewSolanaLive(chainpkg.SolanaConfig{
					ChainIDStr:    ch.ID,
					ChainName:     ch.Name,
					NativeSymbol:  ch.NativeSymbol,
					RPCURL:        rpcURL,
					Confirmations: uint64(ch.RequiredConfirmations),
				})
			default:
				slog.Warn("unknown adapter type, skipping", "chain", ch.ID, "adapter", ch.AdapterType)
				continue
			}
			c.Registry.RegisterChain(adapter)
		}
	}

	c.WebhookService = webhook.NewService(webhook.Deps{
		SQS:     c.SQS,
		Configs: c.WebhookConfigRepo,
		Events:  c.WebhookEventRepo,
	})
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
	c.WebhookService.SetDeliverySettingsSource(func(ctx context.Context) (webhook.DeliverySettings, error) {
		stored, readErr := accountSettings.EffectiveWebhookDelivery(ctx)
		if readErr != nil {
			return webhook.DeliverySettings{}, readErr
		}
		return webhook.DeliverySettingsFromStored(stored.MaxAttempts, stored.TimeoutSeconds), nil
	})
	c.PriceService = buildPriceService(c, accountSettings)
	c.SweepService = sweep.NewService(sweep.Deps{
		Registry:     c.Registry,
		MPC:          c.MPCService,
		Secrets:      sweepsecrets.New(c.SecretsManager),
		Redis:        sweepredis.New(c.Redis),
		Webhook:      c.WebhookService,
		Wallets:      c.WalletRepo,
		Addresses:    c.AddressRepo,
		Transactions: c.TransactionRepo,
		SweepLimits:  accountSettings.EffectiveSweepLimits,
		Chains:       c.ChainRepo,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		},
		GasDefaults:    nil,
		TokenPricer:    c.PriceService,
		DustUSDDefault: nil,
	})
	c.WithdrawalService = withdraw.NewService(withdraw.Deps{
		Registry:     c.Registry,
		Webhook:      c.WebhookService,
		MPC:          c.MPCService,
		Locker:       redislock.New(c.Redis),
		Transactions: c.TransactionRepo,
		Wallets:      c.WalletRepo,
		Addresses:    c.AddressRepo,
		Sweep:        c.SweepService,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused)
		},
	})
	c.WithdrawalService.UseUSDQuote(c.PriceService)
	verifier, ok := c.SecondFactor.(*authsvc.SecondFactorVerifier)
	if !ok || verifier == nil {
		return nil, fmt.Errorf("vault: withdrawal create: second factor verifier is required")
	}
	withdrawalRows, err := resolve[*withdrawalrecords.Records](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal create: %w", err)
	}
	c.WithdrawalService.UseCreate(c.UserRepo, verifier, withdrawalRows, c.ChainRepo)

	blockHeightProviders := blockheight.NewProviders(blockheight.ProvidersDeps{
		Key: func(ctx context.Context) string {
			envKey := facades.Config().GetString("vault.webhooks.etherscan_api_key")
			return accountSettings.EtherscanKeyForHeight(ctx, envKey)
		},
		NetworkByChain: networkByChain,
		Blockstream:    blockstreamtip.New(),
		Testnet4:       mempooltip.New(),
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

	slog.Info("vault container booted", "chains", c.Registry.ChainIDs())
	return c, nil
}

// buildWebhookIngest keeps provider credentials out of the boot snapshot.
// Each provider reads its KeySource when it calls the vendor or verifies a
// webhook. webhooksync asks again on every sync. A missing or unusable
// settings row falls back to vault.webhooks.*; an empty result fails closed.
func buildWebhookIngest(c *container.Container, accountSettings *settings.Service) {
	keyFor := func(ctx context.Context, provider string) string {
		envKey := facades.Config().GetString(ingestEnvConfigKey(provider))
		if accountSettings == nil {
			return envKey
		}
		return accountSettings.IngestProviderKey(ctx, provider, envKey)
	}
	providerMap := map[string]providers.WebhookProvider{
		"alchemy":   providers.NewAlchemyProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "alchemy") }),
		"helius":    providers.NewHeliusProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "helius") }),
		"quicknode": providers.NewQuickNodeProvider("").UseKeySource(func(ctx context.Context) string { return keyFor(ctx, "quicknode") }),
	}
	c.WebhookProviders = providerMap
	syncers := make(map[string]webhooksync.AddressSyncer, len(providerMap))
	for name, provider := range providerMap {
		syncers[name] = provider
	}
	c.WebhookSyncService = webhooksync.NewService(webhooksync.Deps{
		Subscriptions: c.WebhookSubscriptionRepo,
		Addresses:     c.AddressRepo,
		Providers:     syncers,
		ProviderKey:   keyFor,
	})
}

func ingestEnvConfigKey(provider string) string {
	switch provider {
	case "alchemy":
		return "vault.webhooks.alchemy_auth_token"
	case "helius":
		return "vault.webhooks.helius_api_key"
	case "quicknode":
		return "vault.webhooks.quicknode_api_key"
	default:
		return ""
	}
}

// buildPriceService quotes through price.SettingsSource on each refresh.
// Provider keys are opened then, not copied into clients at boot. When no
// settings provider is usable, the quote keeps the environment CoinAPI key.
func buildPriceService(c *container.Container, accountSettings *settings.Service) *price.Service {
	service := price.NewService(price.Deps{
		Currencies: c.CurrencyRepo,
		Cache:      pricecache.New(c.Redis),
	}).
		WithQuoteDialer(coinapiws.Dialer{}).
		WithEnvCoinAPIKey(c.PriceConfig.CoinAPIKey)
	if accountSettings == nil {
		return service
	}
	return service.WithSettingsSource(func(ctx context.Context) ([]price.Credential, error) {
		opened, err := accountSettings.PriceProvidersForQuote(ctx)
		if err != nil {
			return nil, err
		}
		credentials := make([]price.Credential, 0, len(opened))
		for _, item := range opened {
			credentials = append(credentials, price.Credential{Name: item.Name, Key: item.Key})
		}
		return credentials, nil
	})
}

// buildPendingDepositStore keeps failed deposit blocks in Redis and in a local
// append-only file; either one is enough, and with neither the scanner stops before a
// failing block instead of skipping it.
func buildPendingDepositStore(rdb *redis.Client, dir string) pending.Store {
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
// so the scanner retries it (chain.EVMConfig.StrictLogScan).
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
