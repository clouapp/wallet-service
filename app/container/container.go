package container

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/redis/go-redis/v9"

	chainpkg "github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/deposit"
	mpc "github.com/macromarkets/vault/app/services/mpc"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/app/services/wallet"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/app/services/withdraw"
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

	Registry          *chainpkg.Registry
	WalletService     *wallet.Service
	DepositService    *deposit.Service
	WithdrawalService *withdraw.Service
	WebhookService    *webhook.Service
}

var globalContainer *Container

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

	// --- Services ---
	c.WebhookService = webhook.NewService(c.SQS)
	c.WalletService = wallet.NewService(c.Registry, c.Redis, c.MPCService, c.SecretsManager)
	c.WithdrawalService = withdraw.NewService(c.Registry, c.WebhookService)
	c.DepositService = deposit.NewService(c.Redis, c.Registry, c.WebhookService)

	globalContainer = c
	slog.Info("container booted", "chains", c.Registry.ChainIDs())
	return c
}

// Get returns the global container instance.
func Get() *Container {
	return globalContainer
}
