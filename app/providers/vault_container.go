package providers

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/config"
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
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
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

	awsCfg, err := config.LoadDefaultConfig(context.Background())
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

	activeChains, chainErr := c.ChainRepo.FindActive()
	if chainErr != nil {
		slog.Error("failed to load chains from DB", "error", chainErr)
	} else {
		for _, ch := range activeChains {
			rpcURL, decErr := facades.Crypt().DecryptString(ch.RpcURL)
			if decErr != nil {
				slog.Warn("failed to decrypt RPC URL, skipping chain", "chain", ch.ID, "error", decErr)
				continue
			}
			if rpcURL == "" {
				slog.Warn("empty RPC URL, skipping chain", "chain", ch.ID)
				continue
			}
			var adapter types.Chain
			switch ch.AdapterType {
			case models.AdapterTypeEVM:
				networkID := int64(0)
				if ch.NetworkID != nil {
					networkID = *ch.NetworkID
				}
				adapter = chainpkg.NewEVMLive(chainpkg.EVMConfig{
					ChainIDStr:    ch.ID,
					ChainName:     ch.Name,
					NativeSymbol:  ch.NativeSymbol,
					NativeDecimal: uint8(ch.NativeDecimals),
					NetworkID:     networkID,
					RPCURL:        rpcURL,
					Confirmations: uint64(ch.RequiredConfirmations),
					ERC20Tokens:   tokensByChain[ch.ID],
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
	c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis, c.TransactionRepo, c.WalletRepo)

	etherscanKey := facades.Config().GetString("vault.webhooks.etherscan_api_key")
	blockHeightProviders := map[string]blockheight.Provider{
		models.AdapterTypeEVM:     blockheight.NewEtherscanProvider(etherscanKey),
		models.AdapterTypeBitcoin: blockheight.NewBlockstreamProvider(),
		models.AdapterTypeSolana:  blockheight.NewSolanaPublicProvider(),
	}
	c.DepositService = deposit.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo, blockHeightProviders)
	c.IngestService = ingest.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)
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
