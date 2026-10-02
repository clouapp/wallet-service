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
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/config"
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

func registerVaultContainer(app foundation.Application) {
	app.Singleton(container.ContainerKey, func(_ foundation.Application) (any, error) {
		c, err := buildVaultContainer()
		if err != nil {
			return nil, err
		}
		return c, nil
	})
}

func buildVaultContainer() (*container.Container, error) {
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
	c.SQS = queue.NewSQSClient(sqsClient, queue.QueueURLs{
		Webhook: facades.Config().GetString("vault.queues.webhook"),
	})

	smClient := secretsmanager.NewFromConfig(awsCfg)
	if endpoint := facades.Config().GetString("vault.aws.endpoint_url"); endpoint != "" {
		smClient = secretsmanager.NewFromConfig(awsCfg,
			secretsmanager.WithEndpointResolverV2(staticEndpointResolver{url: endpoint}))
	}
	c.SecretsManager = smClient
	c.MPCService = mpc.NewTSSService()

	c.UserRepo = repositories.NewUserRepository()
	c.RefreshTokenRepo = repositories.NewRefreshTokenRepository()
	c.PasswordResetTokenRepo = repositories.NewPasswordResetTokenRepository()
	c.TotpRecoveryCodeRepo = repositories.NewTotpRecoveryCodeRepository()
	c.AccountRepo = repositories.NewAccountRepository()
	c.AccountUserRepo = repositories.NewAccountUserRepository()
	c.AccessTokenRepo = repositories.NewAccessTokenRepository()
	c.WalletRepo = repositories.NewWalletRepository()
	c.WalletUserRepo = repositories.NewWalletUserRepository()
	c.AddressRepo = repositories.NewAddressRepository()
	c.TransactionRepo = repositories.NewTransactionRepository()
	c.WithdrawalRepo = repositories.NewWithdrawalRepository()
	c.WebhookConfigRepo = repositories.NewWebhookConfigRepository()
	c.WebhookEventRepo = repositories.NewWebhookEventRepository()
	c.WhitelistEntryRepo = repositories.NewWhitelistEntryRepository()
	c.ChainRepo = repositories.NewChainRepository()
	c.TokenRepo = repositories.NewTokenRepository()
	c.ChainResourceRepo = repositories.NewChainResourceRepository()
	c.WebhookSubscriptionRepo = repositories.NewWebhookSubscriptionRepository()
	c.WalletAssetBalanceRepo = repositories.NewWalletAssetBalanceRepository()
	c.WalletBalanceSnapshotRepo = repositories.NewWalletBalanceSnapshotRepository()
	c.WalletUTXORepo = repositories.NewWalletUTXORepository()
	c.WalletSyncStateRepo = repositories.NewWalletSyncStateRepository()
	c.CurrencyRepo = repositories.NewCurrencyRepository()

	if err := wireAuthServices(c); err != nil {
		return nil, err
	}

	c.PriceConfig.CoinGeckoAPIKey = facades.Config().GetString("vault.price.coingecko_api_key")
	c.PriceConfig.CoinMarketCapAPIKey = facades.Config().GetString("vault.price.coinmarketcap_api_key")
	c.PriceConfig.CoinAPIKey = facades.Config().GetString("vault.price.coinapi_key")

	providerMap := make(map[string]providers.WebhookProvider)
	if token := facades.Config().GetString("vault.webhooks.alchemy_auth_token"); token != "" {
		providerMap["alchemy"] = providers.NewAlchemyProvider(token)
	}
	if key := facades.Config().GetString("vault.webhooks.helius_api_key"); key != "" {
		providerMap["helius"] = providers.NewHeliusProvider(key)
	}
	if key := facades.Config().GetString("vault.webhooks.quicknode_api_key"); key != "" {
		providerMap["quicknode"] = providers.NewQuickNodeProvider(key)
	}
	c.WebhookProviders = providerMap
	c.WebhookSyncService = webhooksync.NewService(c.WebhookSubscriptionRepo, c.AddressRepo, providerMap)

	c.Registry = chainpkg.NewRegistry()

	tokensByChain := make(map[string][]types.Token)
	activeTokens, tokenErr := c.TokenRepo.FindActive()
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
	activeChains, chainErr := c.ChainRepo.FindActive()
	if chainErr != nil {
		slog.Error("failed to load chains from DB", "error", chainErr)
	} else {
		for _, ch := range activeChains {
			storedURL, decErr := facades.Crypt().DecryptString(ch.RpcURL)
			if decErr != nil {
				slog.Warn("failed to decrypt RPC URL, skipping chain", "chain", ch.ID, "error", decErr)
				continue
			}
			rpcURL, resolveErr := models.ResolveRPCURL(storedURL)
			if resolveErr != nil {
				slog.Warn("failed to resolve RPC URL, skipping chain", "chain", ch.ID, "error", resolveErr)
				continue
			}
			if rpcURL == "" {
				slog.Warn("empty RPC URL, skipping chain", "chain", ch.ID)
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

	c.WebhookService = webhook.NewService(c.SQS, c.WebhookConfigRepo, c.WebhookEventRepo)
	c.WalletService = wallet.NewService(c.Registry, c.Redis, c.MPCService, c.SecretsManager, c.WalletRepo, c.AddressRepo)
	c.WalletService.SetWebhookSync(c.WebhookSyncService)
	c.SweepService = sweep.NewService(
		c.Registry, c.MPCService, c.SecretsManager, c.Redis, c.WebhookService,
		c.WalletRepo, c.AddressRepo, c.TransactionRepo, c.AccountRepo, c.ChainRepo,
	)
	c.WithdrawalService = withdraw.NewService(
		c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis,
		c.TransactionRepo, c.WalletRepo, c.AddressRepo, c.SweepService,
	)

	etherscanKey := facades.Config().GetString("vault.webhooks.etherscan_api_key")
	blockHeightProviders := blockheight.NewProviders(etherscanKey, networkByChain)
	assetDecimals := withdrawalevents.NewRegistryDecimals(c.Registry, c.ChainRepo)
	c.WithdrawalEvents = withdrawalevents.NewPublisher(
		c.WebhookService, c.WithdrawalRepo, c.TransactionRepo, c.WalletRepo, assetDecimals,
	)
	c.DepositEvents = depositevents.NewPublisher(c.WebhookService, c.WalletRepo, assetDecimals)
	c.DepositService = deposit.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo, blockHeightProviders)
	c.DepositService.SetWithdrawalConfirmations(c.WithdrawalEvents)
	c.DepositService.SetDepositEvents(c.DepositEvents)
	scanOptions, err := deposit.ScanOptionsFromSettings(
		facades.Config().GetInt("vault.deposit_scan.batch_blocks"),
		facades.Config().GetInt("vault.deposit_scan.catch_up_blocks"),
		facades.Config().GetInt("vault.deposit_scan.concurrency"),
	)
	if err != nil {
		return nil, fmt.Errorf("vault: deposit scan options: %w", err)
	}
	if err := c.DepositService.SetScanOptions(scanOptions); err != nil {
		return nil, fmt.Errorf("vault: deposit scan options: %w", err)
	}
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
	c.PendingDeposits = buildPendingDepositStore(c.Redis, facades.Config().GetString("vault.deposit_scan.pending_dir"))
	if c.PendingDeposits != nil {
		c.DepositService.SetPendingStore(c.PendingDeposits)
	}
	c.IngestService = ingest.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)
	c.IngestService.SetDepositEvents(c.DepositEvents)
	c.BalanceRefreshService = refresh.NewBalanceService(
		c.Registry,
		c.WalletRepo,
		c.WalletAssetBalanceRepo,
		c.WalletBalanceSnapshotRepo,
		c.WalletSyncStateRepo,
	)
	walletRefresher, err := refresh.NewWalletRefresher(
		c.BalanceRefreshService, c.WalletRepo, c.Registry,
		time.Duration(facades.Config().GetInt("vault.local_workers.balance_refresh_spacing_ms"))*time.Millisecond,
	)
	if err != nil {
		return nil, fmt.Errorf("vault: wallet refresher: %w", err)
	}
	c.WalletRefresher = walletRefresher
	c.DepositService.SetBalanceRefresher(c.WalletRefresher)

	var priceProviders []price.PriceProvider
	if key := c.PriceConfig.CoinGeckoAPIKey; key != "" {
		priceProviders = append(priceProviders, price.NewCoinGeckoProvider(key))
	}
	if key := c.PriceConfig.CoinMarketCapAPIKey; key != "" {
		priceProviders = append(priceProviders, price.NewCoinMarketCapProvider(key))
	}
	if key := c.PriceConfig.CoinAPIKey; key != "" {
		priceProviders = append(priceProviders, price.NewCoinAPIProvider(key))
	}
	c.PriceService = price.NewService(priceProviders, c.CurrencyRepo, c.Redis)

	slog.Info("vault container booted", "chains", c.Registry.ChainIDs())
	return c, nil
}

// buildPendingDepositStore keeps failed deposit blocks in Redis and in a local
// append-only file; either one is enough, and with neither the scanner stops before a
// failing block instead of skipping it.
func buildPendingDepositStore(rdb *redis.Client, dir string) pending.Store {
	var redisStore *pending.RedisStore
	if rdb != nil {
		store, err := pending.NewRedisStore(rdb, pending.DefaultRedisKeyPrefix)
		if err != nil {
			slog.Error("vault: pending deposit redis store unavailable", "error", err)
		} else {
			redisStore = store
		}
	}
	if dir == "" {
		dir = defaultPendingDepositDir()
	}
	fileStore, err := pending.NewFileStore(dir)
	if err != nil {
		slog.Error("vault: pending deposit file store unavailable", "dir", dir, "error", err)
		fileStore = nil
	}
	store, err := pending.NewDurableStore(redisStore, fileStore)
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

// resolveGasReadinessThreshold returns the gas-readiness threshold for a chain,
// preferring the value seeded on the chains row, falling back to config.SweepDefaults.
// Returns nil if neither source provides a value (e.g. BTC).
func resolveGasReadinessThreshold(ch *models.Chain) *big.Int {
	if v := ch.GasReadinessThreshold(); v != nil {
		return v
	}
	defaults := config.SweepDefaults()
	if d, ok := defaults[ch.ID]; ok && d.GasReadinessRaw != "" {
		if v, ok := new(big.Int).SetString(d.GasReadinessRaw, 10); ok {
			return v
		}
	}
	return nil
}

// resolveDustThresholdNative returns the native dust threshold for a chain,
// preferring the chains row, falling back to config.SweepDefaults.
func resolveDustThresholdNative(ch *models.Chain) *big.Int {
	if v := ch.DustThresholdNative(); v != nil {
		return v
	}
	defaults := config.SweepDefaults()
	if d, ok := defaults[ch.ID]; ok && d.DustNativeRaw != "" {
		if v, ok := new(big.Int).SetString(d.DustNativeRaw, 10); ok {
			return v
		}
	}
	return nil
}
