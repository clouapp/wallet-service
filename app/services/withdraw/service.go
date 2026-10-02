package withdraw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/webhook"
)

// Sentinel errors for HTTP response mapping in controller.
var (
	ErrInvalidPassphrase  = errors.New("invalid passphrase")
	ErrInsufficientFunds  = errors.New("insufficient funds")
	ErrConcurrentWithdraw = errors.New("withdrawal already in progress for this wallet")
	ErrPassphraseTooShort = errors.New("passphrase must be at least 12 characters")
	ErrTooManyAttempts    = errors.New("too many failed attempts, try again later")
)

type Service struct {
	registry        *chainpkg.Registry
	webhookSvc      *webhook.Service
	mpc             mpcpkg.Service
	secrets         *secretsmanager.Client
	rdb             *redis.Client
	transactionRepo repositories.TransactionRepository
	walletRepo      *repositories.WalletRepository
	addressRepo     *repositories.AddressRepository
	sweep           sweep.Service
}

func NewService(
	registry *chainpkg.Registry,
	webhookSvc *webhook.Service,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	transactionRepo repositories.TransactionRepository,
	walletRepo *repositories.WalletRepository,
	addressRepo *repositories.AddressRepository,
	sweepSvc sweep.Service,
) *Service {
	return &Service{
		registry:        registry,
		webhookSvc:      webhookSvc,
		mpc:             mpc,
		secrets:         secrets,
		rdb:             rdb,
		transactionRepo: transactionRepo,
		walletRepo:      walletRepo,
		addressRepo:     addressRepo,
		sweep:           sweepSvc,
	}
}

// WithdrawRequest is the input to Service.Request.
//
// Source selection is now delegated entirely to sweep.Service — the caller no
// longer picks which address the funds come from. The planner decides based on
// on-chain balances (direct_from_base, direct_from_child, or multi_sweep).
//
// CallerAccountID is the authenticated caller's account (dashboard session or
// API token), threaded through to the sweep planner so per-caller limits and
// quotas apply correctly on shared wallets. Controllers must populate this
// from the request context.
type WithdrawRequest struct {
	WalletID        uuid.UUID `json:"wallet_id"`
	ExternalUserID  string    `json:"external_user_id"`
	ToAddress       string    `json:"to_address"`
	Amount          string    `json:"amount"`
	Asset           string    `json:"asset"`
	Passphrase      string    `json:"passphrase"`
	IdempotencyKey  string    `json:"idempotency_key"`
	CallerAccountID uuid.UUID `json:"-"`
}

// Metadata describes the source-selection outcome for a Request. Emitted
// alongside the final Transaction so controllers can surface sweep context
// (strategy, completed legs) to API consumers.
type Metadata struct {
	Strategy   sweep.Strategy         `json:"strategy,omitempty"`
	Sweeps     []sweep.CompletedSweep `json:"sweeps,omitempty"`
	FailedStep *sweep.FailedStep      `json:"failed_step,omitempty"`
}

// Request runs a withdrawal end-to-end: validation, plan, execute, report.
//
// EVM multi-sweep still requires a seeded gas balance. SOL and BTC adapters
// report no gas threshold, so that check does not apply to them.
func (s *Service) Request(ctx context.Context, req WithdrawRequest) (*models.Transaction, *Metadata, error) {
	if len(req.Passphrase) < 12 {
		return nil, nil, ErrPassphraseTooShort
	}

	if req.IdempotencyKey != "" {
		existing, err := s.transactionRepo.FindByIdempotencyKey(req.IdempotencyKey)
		if err == nil && existing != nil {
			return existing, &Metadata{}, nil
		}
	}

	if s.rdb == nil {
		return nil, nil, fmt.Errorf("redis lock: redis is not configured")
	}
	lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
	acquired, err := s.rdb.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("redis lock: %w", err)
	}
	if !acquired {
		return nil, nil, ErrConcurrentWithdraw
	}
	defer s.rdb.Del(ctx, lockKey)

	walletPtr, err := s.walletRepo.FindByID(ctx, req.WalletID)
	if err != nil || walletPtr == nil {
		return nil, nil, fmt.Errorf("wallet not found")
	}
	wallet := *walletPtr

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, nil, err
	}
	if !adapter.ValidateAddress(req.ToAddress) {
		return nil, nil, fmt.Errorf("invalid address for chain %s", wallet.Chain)
	}

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		return nil, nil, fmt.Errorf("invalid amount: %s", req.Amount)
	}

	if err := s.checkRateLimit(ctx, req.WalletID.String()); err != nil {
		return nil, nil, err
	}

	plan, err := s.sweep.PlanForWithdrawal(ctx, wallet.ID, req.Asset, amount, req.ToAddress, req.CallerAccountID)
	if err != nil {
		if errors.Is(err, sweep.ErrUnsupportedChain) {
			// Bubble the sentinel; the controller maps it to 422
			// unsupported_chain via MapSweepError.
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("plan withdrawal: %w", err)
	}
	if plan == nil {
		return nil, nil, fmt.Errorf("plan withdrawal: nil plan")
	}

	switch plan.Strategy {
	case sweep.StrategyInsufficient:
		return nil, nil, sweep.ErrInsufficientFunds
	case sweep.StrategyMultiSweep:
		adapter, adapterErr := s.registry.Chain(wallet.Chain)
		if adapterErr != nil {
			return nil, nil, adapterErr
		}
		if adapter.GasReadinessThreshold() != nil && wallet.GasStatus != models.GasStatusSeeded {
			return nil, nil, sweep.ErrWalletNotGasReady
		}
	}

	shareA, err := s.decryptShareA(ctx, &wallet, req.Passphrase)
	if err != nil {
		return nil, nil, err
	}
	defer zeroShare(shareA)

	withdrawalTxID := uuid.New()
	creds := sweep.SigningCredentials{ShareA: shareA, Passphrase: req.Passphrase}
	result, err := s.sweep.ExecutePlan(ctx, plan, creds, withdrawalTxID, req.ToAddress, req.ExternalUserID)
	if err != nil {
		return nil, nil, fmt.Errorf("execute plan: %w", err)
	}
	if result == nil {
		return nil, nil, fmt.Errorf("execute plan: nil result")
	}

	meta := &Metadata{
		Strategy: plan.Strategy,
		Sweeps:   result.Sweeps,
	}

	if result.FailedStep != nil {
		meta.FailedStep = result.FailedStep
		return nil, meta, fmt.Errorf(
			"sweep leg %d failed: %s",
			result.FailedStep.Index, result.FailedStep.LastError,
		)
	}
	if result.FinalWithdrawTx == nil {
		return nil, meta, fmt.Errorf("execute plan: no final withdrawal tx produced")
	}

	finalTx := result.FinalWithdrawTx

	// Persist the idempotency key on the final withdrawal tx. The executor
	// creates the row without this field so we only attach it here once we
	// know the plan executed to completion. The column is nullable with a
	// partial unique index, so intermediate sweep/gas_seed rows store NULL
	// and never collide with each other.
	if req.IdempotencyKey != "" {
		idemKey := req.IdempotencyKey
		finalTx.IdempotencyKey = &idemKey
		if err := s.transactionRepo.UpdateFields(finalTx.ID, map[string]interface{}{
			"idempotency_key": req.IdempotencyKey,
		}); err != nil {
			slog.Warn("failed to set idempotency_key on final withdrawal tx",
				"tx_id", finalTx.ID, "error", err)
		}
	}

	// The sweep executor already enqueues EventWithdrawalBroadcasting for the
	// final tx — do not re-emit here. The public withdrawal.broadcast event is
	// published by withdrawalevents.Publisher once the withdrawal row is
	// marked broadcast. We still dispatch the Goravel domain
	// event so wallet-refresh listeners fire.
	_ = facades.Event().Job(&events.WithdrawalBroadcasted{}, []event.Arg{
		{Type: "string", Value: finalTx.WalletID.String()},
		{Type: "string", Value: wallet.Chain},
	}).Dispatch()

	slog.Info("withdrawal broadcast",
		"tx_id", finalTx.ID,
		"tx_hash", finalTx.TxHash,
		"chain", wallet.Chain,
		"strategy", plan.Strategy,
		"sweeps", len(result.Sweeps),
	)
	return finalTx, meta, nil
}

// decryptShareA decrypts the wallet's MPC customer share (share A) using the
// passphrase. On bad passphrase it records a failed attempt for rate-limiting
// and returns ErrInvalidPassphrase. Callers are responsible for zeroing the
// returned slice once they are done signing.
func (s *Service) decryptShareA(ctx context.Context, wallet *models.Wallet, passphrase string) ([]byte, error) {
	shareA, err := wallet.DecryptShareA(passphrase)
	if err != nil {
		if errors.Is(err, mpcpkg.ErrInvalidPassphrase) {
			s.recordFailedAttempt(ctx, wallet.ID.String())
			return nil, ErrInvalidPassphrase
		}
		return nil, err
	}
	return shareA, nil
}

func (s *Service) checkRateLimit(ctx context.Context, walletID string) error {
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", walletID)
	count, err := s.rdb.Get(ctx, key).Int()
	if err != nil && err != redis.Nil {
		return nil
	}
	if count >= 5 {
		return ErrTooManyAttempts
	}
	return nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, walletID string) {
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", walletID)
	pipe := s.rdb.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 60*time.Second)
	_, _ = pipe.Exec(ctx)
}

// zeroShare wipes a byte slice in place so sensitive key material does not
// linger in memory longer than necessary.
func zeroShare(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ---------------------------------------------------------------------------
// Query helpers (used by API controllers)
// ---------------------------------------------------------------------------

func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	tx, err := s.transactionRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return s.transactionRepo.List(chainID, txType, status, userID, limit, offset)
}

// ListTransactionsForAccount is the account-scoped variant used by external API
// endpoints that accept a customer-supplied filter (e.g. external_user_id).
// Without this scoping, any API token could retrieve transactions for another
// account by guessing or enumerating external_ids.
func (s *Service) ListTransactionsForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return s.transactionRepo.ListForAccount(accountID, chainID, txType, status, userID, limit, offset)
}
