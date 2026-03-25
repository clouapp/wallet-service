package container

import (
	"context"
	"log/slog"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/redis/go-redis/v9"
	smithyendpoints "github.com/aws/smithy-go/endpoints"

	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// ---------------------------------------------------------------------------
// Container — single dependency graph, built once per Lambda cold start.
// Every service gets what it needs via constructor injection.
// ---------------------------------------------------------------------------

type Container struct {
	Redis           *redis.Client
	SQS             *queue.SQSClient
	SecretsManager  *secretsmanager.Client
	MPCService      mpc.Service

	UserRepo               repositories.UserRepository
	RefreshTokenRepo       repositories.RefreshTokenRepository
	PasswordResetTokenRepo repositories.PasswordResetTokenRepository
	TotpRecoveryCodeRepo   repositories.TotpRecoveryCodeRepository
	AccountRepo            repositories.AccountRepository
	AccountUserRepo        repositories.AccountUserRepository
	AccessTokenRepo        repositories.AccessTokenRepository
	WalletRepo             repositories.WalletRepository
	WalletUserRepo         repositories.WalletUserRepository
	AddressRepo            repositories.AddressRepository
	TransactionRepo        repositories.TransactionRepository
	WithdrawalRepo         repositories.WithdrawalRepository
	WebhookConfigRepo      repositories.WebhookConfigRepository
	WebhookEventRepo       repositories.WebhookEventRepository
	WhitelistEntryRepo     repositories.WhitelistEntryRepository

	Registry          *chainpkg.Registry
	WalletService     *wallet.Service
	DepositService    *deposit.Service
	WithdrawalService *withdraw.Service
	WebhookService    *webhook.Service
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

	// --- Chain Registry ---
	c.Registry = chainpkg.NewRegistry()
	c.Registry.RegisterChain(chainpkg.NewEVMLive(chainpkg.EVMConfig{
		ChainIDStr:    "eth",
		ChainName:     "Ethereum",
		NativeSymbol:  "eth",
		NativeDecimal: 18,
		NetworkID:     1,
		RPCURL:        os.Getenv("ETH_RPC_URL"),
		Confirmations: 12,
	}))
	c.Registry.RegisterChain(chainpkg.NewEVMLive(chainpkg.EVMConfig{
		ChainIDStr:    "polygon",
		ChainName:     "Polygon",
		NativeSymbol:  "matic",
		NativeDecimal: 18,
		NetworkID:     137,
		RPCURL:        os.Getenv("POLYGON_RPC_URL"),
		Confirmations: 128,
	}))
	c.Registry.RegisterChain(chainpkg.NewSolanaLive(os.Getenv("SOLANA_RPC_URL")))
	c.Registry.RegisterChain(chainpkg.NewBitcoinLive(chainpkg.BitcoinConfig{
		RPCURL:  os.Getenv("BTC_RPC_URL"),
		Network: "mainnet",
	}))

	// Tokens
	for _, t := range chainpkg.AllTokens() {
		c.Registry.RegisterToken(t)
	}

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

	// --- Services ---
	c.WebhookService = webhook.NewService(c.SQS, c.WebhookConfigRepo, c.WebhookEventRepo)
	c.WalletService = wallet.NewService(c.Registry, c.Redis, c.MPCService, c.SecretsManager, c.WalletRepo, c.AddressRepo)
	c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService, c.MPCService, c.SecretsManager, c.Redis, c.TransactionRepo, c.WalletRepo)
	c.DepositService = deposit.NewService(c.Redis, c.Registry, c.WebhookService, c.AddressRepo, c.TransactionRepo)

	globalContainer = c
	slog.Info("container booted", "chains", c.Registry.ChainIDs())
	return c
}

// Get returns the global container instance.
func Get() *Container {
	return globalContainer
}
