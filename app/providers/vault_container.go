package providers

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/features"
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
	c.WalletService = wallet.NewService(wallet.Deps{
		Registry:    c.Registry,
		Redis:       c.Redis,
		MPC:         c.MPCService,
		Secrets:     c.SecretsManager,
		Wallets:     c.WalletRepo,
		Addresses:   c.AddressRepo,
		WebhookSync: c.WebhookSyncService,
	})
	flags, err := container.Make[*features.Service]()
	if err != nil {
		return nil, fmt.Errorf("vault: feature flags: %w", err)
	}
	c.SweepService = sweep.NewService(
		c.Registry, c.MPCService, c.SecretsManager, c.Redis, c.WebhookService,
		c.WalletRepo, c.AddressRepo, c.TransactionRepo, c.AccountRepo, c.ChainRepo,
		func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		},
		sweepGasDefaults(),
	)
	c.WithdrawalService = withdraw.NewService(
		c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis,
		c.TransactionRepo, c.WalletRepo, c.AddressRepo, c.SweepService,
		func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused)
		},
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
	c.IngestService = ingest.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)
	c.IngestService.SetDepositEvents(c.DepositEvents)
	c.BalanceRefreshService = refresh.NewBalanceService(
		c.Registry,
		c.WalletRepo,
		c.WalletAssetBalanceRepo,
		c.WalletBalanceSnapshotRepo,
		c.WalletSyncStateRepo,
	)

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

// sweepGasDefaults copies the gas-readiness fallbacks SweepDefaults already
// reads. The sweep service receives the values and does not import config.
func sweepGasDefaults() map[string]sweep.GasReadinessDefault {
	return gasReadinessDefaultsFrom(config.SweepDefaults())
}

func gasReadinessDefaultsFrom(configured map[string]config.SweepThresholds) map[string]sweep.GasReadinessDefault {
	if len(configured) == 0 {
		return nil
	}
	out := make(map[string]sweep.GasReadinessDefault, len(configured))
	for chainID, thresholds := range configured {
		out[chainID] = sweep.GasReadinessDefault{Raw: thresholds.GasReadinessRaw}
	}
	return out
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
