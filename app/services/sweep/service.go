package sweep

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
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
	// ErrGasEstimateFailed is chain.ErrGasEstimateFailed, re-exported for controllers.
	ErrGasEstimateFailed = chain.ErrGasEstimateFailed
)

// Service coordinates withdrawal planning, sweep execution, and gas-readiness tracking.
//
// `callerAccountID` threads the caller's (not the wallet's) account through
// quota enforcement. A shared wallet can have multiple account members, and a
// per-caller counter prevents one caller from burning the whole account's
// daily quota. Pass uuid.Nil from non-authenticated contexts (tests,
// background workers) to fall back to default limits with no per-account
// quota increment.
type Service interface {
	PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int, toAddress string, callerAccountID uuid.UUID) (*Plan, error)
	ExecutePlan(ctx context.Context, plan *Plan, creds SigningCredentials, withdrawalTxID uuid.UUID, toAddress string, externalUserID string) (*Result, error)
	ConsolidateAll(ctx context.Context, walletID uuid.UUID, asset string, passphrase string, callerAccountID uuid.UUID) (*Result, error)
	RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error)
	LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error)
}

// accountReader is the account lookup sweep uses for per-account limits.
type accountReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error)
}

// walletReader is the wallet lookup and gas-status write sweep uses.
type walletReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	RecordGasCheck(ctx context.Context, id uuid.UUID, checkedAt time.Time, status string, updateStatus bool) error
}

// addressReader is the child-address lookup sweep uses.
type addressReader interface {
	FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.Address, error)
}

// transactionWriter is the row sweep persists after a broadcast. Signing does not go through it.
type transactionWriter interface {
	Create(ctx context.Context, tx *models.Transaction) error
}

// chainReader loads the chain record sweep needs for adapter and decimals.
type chainReader interface {
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

type service struct {
	registry    *chain.Registry
	mpc         mpcpkg.Service
	secrets     *secretsmanager.Client
	rdb         *redis.Client
	webhookSvc  *webhook.Service
	walletRepo  walletReader
	addressRepo addressReader
	txRepo      transactionWriter
	accountRepo accountReader
	chainRepo   chainReader

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
	walletRepo walletReader,
	addressRepo addressReader,
	txRepo transactionWriter,
	accountRepo accountReader,
	chainRepo chainReader,
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

// loadChain returns the chain, or (nil, nil) when the row is missing, matching
// the previous repository miss. A database error is returned as-is.
func (s *service) loadChain(ctx context.Context, chainID string) (*models.Chain, error) {
	chainEntity, err := s.chainRepo.FindByID(ctx, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, nil
	}
	return chainEntity, err
}

// Concrete method implementations live alongside their domain:
// - PlanForWithdrawal  → planner.go
// - ExecutePlan        → executor.go
// - ConsolidateAll     → consolidate.go
// - RefreshGasStatus   → gas_readiness.go
// - LoadLimits         → limits.go
