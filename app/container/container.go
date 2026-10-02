package container

import (
	"log/slog"
	"os"
	"sync"

	"github.com/goravel/framework/facades"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	mpc 	"github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/webhooksync"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/redis/go-redis/v9"
)

// Container holds the WaaS dependency graph, resolved once from the Goravel service container.
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
	WebhookSubscriptionRepo   repositories.WebhookSubscriptionRepository
	WalletAssetBalanceRepo    repositories.WalletAssetBalanceRepository
	WalletBalanceSnapshotRepo repositories.WalletBalanceSnapshotRepository
	WalletUTXORepo            repositories.WalletUTXORepository
	WalletSyncStateRepo       repositories.WalletSyncStateRepository
	CurrencyRepo              repositories.CurrencyRepository
	WebhookProviders          map[string]providers.WebhookProvider
	WebhookSyncService        *webhooksync.Service

	PriceService *price.Service
	PriceConfig  struct {
		CoinGeckoAPIKey     string
		CoinMarketCapAPIKey string
		CoinAPIKey          string
	}

	Registry              *chainpkg.Registry
	WalletService         *wallet.Service
	DepositService        *deposit.Service
	WithdrawalService     *withdraw.Service
	SweepService          sweep.Service
	WebhookService        *webhook.Service
	WithdrawalEvents      *withdrawalevents.Publisher
	DepositEvents         *depositevents.Publisher
	IngestService         *ingest.Service
	BalanceRefreshService *refresh.BalanceService
	WalletRefresher       *refresh.WalletRefresher
	PendingDeposits       pending.Store

	SecondFactor   *authsvc.SecondFactorVerifier
	TwoFactorLogin *authsvc.TwoFactorLogin
}

var (
	globalContainer *Container
	resolveOnce     sync.Once
)

// Get returns the shared Container, resolving it from the application service container on first use.
func Get() *Container {
	resolveOnce.Do(func() {
		raw, err := facades.App().Make(ContainerKey)
		if err != nil {
			slog.Error("vault: resolve container", "error", err)
			os.Exit(1)
		}
		c, ok := raw.(*Container)
		if !ok || c == nil {
			slog.Error("vault: invalid container binding")
			os.Exit(1)
		}
		globalContainer = c
	})
	return globalContainer
}
