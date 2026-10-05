package sweep

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/types"
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

// accountSweepLimitSource reads the effective account_sweep_limits document
// at the moment of use. A nil source means the registry defaults. The source
// is injected so this package does not query the settings table itself.
type accountSweepLimitSource func(ctx context.Context, accountID uuid.UUID) (settings.SweepLimitValues, error)

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

// SecretReader loads one secret's binary value. The provider supplies it;
// this package never imports the AWS SDK. A nil SecretReader means Secrets
// Manager is not configured. The service keeps the secret id. Bytes are not logged.
type SecretReader interface {
	Binary(ctx context.Context, secretID string) ([]byte, error)
}

// RedisStore runs the wallet-ops lock and the daily consolidate counter.
// The service keeps the keys, the lock value, and the TTLs. A nil RedisStore
// means Redis is not configured.
type RedisStore interface {
	SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error)
	Del(ctx context.Context, key string) error
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
}

// chainLookup is the adapter and token catalog sweep reads.
type chainLookup interface {
	Chain(id string) (types.Chain, error)
	ChainForWallet(wallet *models.Wallet) (types.Chain, error)
	FindToken(chainID, symbol string) (*types.Token, error)
}

// mpcSigner signs a sweep and reconstructs the keys that signature needs.
type mpcSigner interface {
	Sign(ctx context.Context, curve mpcpkg.Curve, shareA, shareB []byte, inputs mpcpkg.SignInputs) ([]byte, error)
	ReconstructEd25519Scalar(shareA, shareB []byte) ([]byte, error)
	ReconstructSecp256k1PrivateKey(shareA, shareB []byte) ([]byte, error)
}

// eventEnqueuer publishes one sweep, withdrawal, or gas-status event.
// A nil enqueuer publishes nothing.
type eventEnqueuer interface {
	EnqueueEvent(ctx context.Context, txID uuid.UUID, eventType types.EventType, data interface{})
}

// accountGate reports a block for one account. Nil means no reader is wired,
// so the action proceeds. The reader is injected: this package cannot import
// the feature-flag service without an import cycle.
type accountGate func(ctx context.Context, accountID uuid.UUID) error

// GasReadinessDefault is the fallback native balance, in raw units, for one
// chain when the chains row has none. An empty Raw means the chain has no
// separate gas asset to monitor.
type GasReadinessDefault struct {
	Raw string
}

type service struct {
	registry    chainLookup
	mpc         mpcSigner
	secrets     SecretReader
	rdb         RedisStore
	webhookSvc  eventEnqueuer
	walletRepo  walletReader
	addressRepo addressReader
	txRepo      transactionWriter
	sweepLimits accountSweepLimitSource
	chainRepo   chainReader
	flags       accountGate
	gasDefaults map[string]GasReadinessDefault
	// dustUSDDefault is unused in production. An unset dust_threshold_usd filters
	// nothing; the environment does not fill it. Tests may inject a substitute.
	dustUSDDefault func(chainID string) decimal.Decimal
	// tokenPricer converts USD dust thresholds to token amounts; nil disables token dust filtering.
	tokenPricer TokenPricer

	// fetchShareBFn retrieves the service share for a wallet. Tests set it to an
	// in-memory stub. Production uses secrets.
	fetchShareBFn func(ctx context.Context, wallet *models.Wallet) ([]byte, error)
}

// Deps is everything the sweep service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Registry       chainLookup
	MPC            mpcSigner
	Secrets        SecretReader
	Redis          RedisStore
	Webhook        eventEnqueuer
	Wallets        walletReader
	Addresses      addressReader
	Transactions   transactionWriter
	SweepLimits    accountSweepLimitSource
	Chains         chainReader
	Flags          accountGate
	GasDefaults    map[string]GasReadinessDefault
	TokenPricer    TokenPricer
	DustUSDDefault func(chainID string) decimal.Decimal
}

// NewService wires the sweep service from Deps. All concrete methods are
// implemented in planner.go / executor.go / gas_readiness.go / limits.go.
func NewService(deps Deps) Service {
	return &service{
		registry:       deps.Registry,
		mpc:            deps.MPC,
		secrets:        deps.Secrets,
		rdb:            deps.Redis,
		webhookSvc:     deps.Webhook,
		walletRepo:     deps.Wallets,
		addressRepo:    deps.Addresses,
		txRepo:         deps.Transactions,
		sweepLimits:    deps.SweepLimits,
		chainRepo:      deps.Chains,
		flags:          deps.Flags,
		gasDefaults:    cloneGasDefaults(deps.GasDefaults),
		dustUSDDefault: deps.DustUSDDefault,
		tokenPricer:    deps.TokenPricer,
	}
}

func cloneGasDefaults(in map[string]GasReadinessDefault) map[string]GasReadinessDefault {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]GasReadinessDefault, len(in))
	for chainID, value := range in {
		out[chainID] = value
	}
	return out
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
