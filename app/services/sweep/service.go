package sweep

import (
	"context"
	"errors"
	"math/big"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/webhook"
)

// Sentinel errors exposed by the sweep service.
var (
	ErrNotImplemented        = errors.New("sweep: not implemented")
	ErrUnsupportedChain      = errors.New("sweep: chain not supported in v1 (EVM-only)")
	ErrWalletNotGasReady     = errors.New("sweep: wallet not gas-ready")
	ErrInsufficientFunds     = errors.New("sweep: insufficient funds across wallet")
	ErrInFlightConsolidation = errors.New("sweep: another consolidation in flight for this wallet")
	ErrDailyQuotaExceeded    = errors.New("sweep: daily consolidation quota exceeded")
	ErrTooManyAddresses      = errors.New("sweep: too many addresses per request")
)

// Service coordinates withdrawal planning, sweep execution, and gas-readiness tracking.
type Service interface {
	PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int) (*Plan, error)
	ExecutePlan(ctx context.Context, plan *Plan, shareA []byte, withdrawalTxID uuid.UUID, toAddress string, externalUserID string) (*Result, error)
	ConsolidateAll(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Result, error)
	RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error)
	LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error)
}

type service struct {
	registry    *chain.Registry
	mpc         mpcpkg.Service
	secrets     *secretsmanager.Client
	rdb         *redis.Client
	webhookSvc  *webhook.Service
	walletRepo  repositories.WalletRepository
	addressRepo repositories.AddressRepository
	txRepo      repositories.TransactionRepository
	accountRepo repositories.AccountRepository
	chainRepo   repositories.ChainRepository

	// fetchShareBFn is the function used to retrieve the service's MPC share for
	// a wallet. In production it targets AWS Secrets Manager; tests override it
	// with a pure in-memory stub to avoid mocking the secretsmanager SDK.
	fetchShareBFn func(ctx context.Context, wallet *models.Wallet) ([]byte, error)
}

// NewService wires the sweep service. All concrete methods are implemented in
// planner.go / executor.go / gas_readiness.go / limits.go (Tasks 15-19).
func NewService(
	registry *chain.Registry,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	webhookSvc *webhook.Service,
	walletRepo repositories.WalletRepository,
	addressRepo repositories.AddressRepository,
	txRepo repositories.TransactionRepository,
	accountRepo repositories.AccountRepository,
	chainRepo repositories.ChainRepository,
) Service {
	return &service{
		registry:    registry,
		mpc:         mpc,
		secrets:     secrets,
		rdb:         rdb,
		webhookSvc:  webhookSvc,
		walletRepo:  walletRepo,
		addressRepo: addressRepo,
		txRepo:      txRepo,
		accountRepo: accountRepo,
		chainRepo:   chainRepo,
	}
}

// Concrete method implementations live alongside their domain:
// - PlanForWithdrawal  → planner.go
// - ExecutePlan        → executor.go
// - ConsolidateAll     → consolidate.go
// - RefreshGasStatus   → gas_readiness.go
// - LoadLimits         → limits.go
