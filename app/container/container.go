package container

import (
	"context"
	"log/slog"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/redis/go-redis/v9"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/types"
)

// ---------------------------------------------------------------------------
// Container — single dependency graph, built once per Lambda cold start.
// Every service gets what it needs via constructor injection.
// ---------------------------------------------------------------------------

type Container struct {
	Redis          *redis.Client
	SQS            *queue.SQSClient
	SecretsManager *secretsmanager.Client
	MPCService     mpc.Service

	UserRepo                repositories.UserRepository
	RefreshTokenRepo        repositories.RefreshTokenRepository
	PasswordResetTokenRepo  repositories.PasswordResetTokenRepository
	TotpRecoveryCodeRepo    repositories.TotpRecoveryCodeRepository
	AccountRepo             repositories.AccountRepository
	AccountUserRepo         repositories.AccountUserRepository
	AccessTokenRepo         repositories.AccessTokenRepository
	WalletRepo              repositories.WalletRepository
	WalletUserRepo          repositories.WalletUserRepository
	AddressRepo             repositories.AddressRepository
	TransactionRepo         repositories.TransactionRepository
	WithdrawalRepo          repositories.WithdrawalRepository
	WebhookConfigRepo       repositories.WebhookConfigRepository
	WebhookEventRepo        repositories.WebhookEventRepository
	WhitelistEntryRepo      repositories.WhitelistEntryRepository
	ChainRepo               repositories.ChainRepository
	TokenRepo               repositories.TokenRepository
	ChainResourceRepo       repositories.ChainResourceRepository
	WebhookSubscriptionRepo repositories.WebhookSubscriptionRepository
	WebhookProviders        map[string]providers.WebhookProvider
	WebhookSyncService      *webhooksync.Service

	Registry          *chainpkg.Registry
	WalletService     *wallet.Service
	DepositService    *deposit.Service
	WithdrawalService *withdraw.Service
	WebhookService    *webhook.Service
	IngestService     *ingest.Service
	IngestHandler     *ingest.Handler
}

var globalContainer *Container

// staticEndpointResolver routes all Secrets Manager calls to a fixed endpoint.
// Used in dev to point at LocalStack.
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

// Boot builds the full dependency graph.
func Boot() *Container {
	c := &Container{}

	// --- Redis (for address cache + checkpoints) ---
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err == nil {
			c.Redis = redis.NewClient(opts)
		}
	}

	// --- AWS Config, SQS, Secrets Manager ---
	awsCfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		slog.Error("aws config failed", "error", err)
		os.Exit(1)
	}
	sqsClient := sqs.NewFromConfig(awsCfg)
	c.SQS = queue.NewSQSClient(sqsClient, queue.QueueURLs{
		Webhook: os.Getenv("WEBHOOK_QUEUE_URL"),
	})

	// --- AWS Secrets Manager ---
	smClient := secretsmanager.NewFromConfig(awsCfg)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		smClient = secretsmanager.NewFromConfig(awsCfg,
			secretsmanager.WithEndpointResolverV2(staticEndpointResolver{url: endpoint}))
	}
	c.SecretsManager = smClient
	c.MPCService = mpc.NewTSSService()

	// --- Repositories ---
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

	providerMap := make(map[string]providers.WebhookProvider)
	if token := os.Getenv("ALCHEMY_AUTH_TOKEN"); token != "" {
		providerMap["alchemy"] = providers.NewAlchemyProvider(token)
	}
	if key := os.Getenv("HELIUS_API_KEY"); key != "" {
		providerMap["helius"] = providers.NewHeliusProvider(key)
	}
	if key := os.Getenv("QUICKNODE_API_KEY"); key != "" {
		providerMap["quicknode"] = providers.NewQuickNodeProvider(key)
	}
	c.WebhookProviders = providerMap
	c.WebhookSyncService = webhooksync.NewService(c.WebhookSubscriptionRepo, c.AddressRepo, providerMap)

	// --- Chain Registry (DB-driven) ---
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
					RPCURL:  rpcURL,
					Network: network,
				})
			case models.AdapterTypeSolana:
				adapter = chainpkg.NewSolanaLive(rpcURL)
			default:
				slog.Warn("unknown adapter type, skipping", "chain", ch.ID, "adapter", ch.AdapterType)
				continue
			}
			c.Registry.RegisterChain(adapter)
		}
	}

	// --- Services ---
	c.WebhookService = webhook.NewService(c.SQS, c.WebhookConfigRepo, c.WebhookEventRepo)
	c.WalletService = wallet.NewService(c.Registry, c.Redis, c.MPCService, c.SecretsManager, c.WalletRepo, c.AddressRepo)
	c.WalletService.SetWebhookSync(c.WebhookSyncService)
	c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis, c.TransactionRepo, c.WalletRepo)
	blockHeightProviders := map[string]blockheight.Provider{
		models.AdapterTypeEVM:     blockheight.NewEtherscanProvider(os.Getenv("ETHERSCAN_API_KEY")),
		models.AdapterTypeBitcoin: blockheight.NewBlockstreamProvider(),
		models.AdapterTypeSolana:  blockheight.NewSolanaPublicProvider(),
	}
	c.DepositService = deposit.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo, blockHeightProviders)
	c.IngestService = ingest.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)
	c.IngestHandler = ingest.NewHandler(c.IngestService, c.WebhookSubscriptionRepo, ingest.DefaultWebhookProviders())

	globalContainer = c
	slog.Info("container booted", "chains", c.Registry.ChainIDs())
	return c
}

// Get returns the global container instance.
func Get() *Container {
	return globalContainer
}
