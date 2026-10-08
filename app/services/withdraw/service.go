package withdraw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/types"
)

// accountGate reports a block for one account. Nil means the caller has no
// flag reader wired, so the action proceeds. The concrete reader lives outside
// this package: importing it would cycle through the container.
type accountGate func(ctx context.Context, accountID uuid.UUID) error

// Sentinel errors for HTTP response mapping in controller.
var (
	ErrInvalidPassphrase   = errors.New("invalid passphrase")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrConcurrentWithdraw  = errors.New("withdrawal already in progress for this wallet")
	ErrPassphraseTooShort  = errors.New("passphrase must be at least 12 characters")
	ErrTooManyAttempts     = errors.New("too many failed attempts, try again later")
	ErrTransactionNotFound = errors.New("transaction not found")
)

// Locker is the withdrawal lock and the passphrase-attempt counter.
// The provider supplies it; this package never imports the Redis client.
// A nil Locker means Redis is not configured.
type Locker interface {
	SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error)
	Del(ctx context.Context, key string) error
	// Int reads a counter. A missing key returns 0 and a nil error.
	Int(ctx context.Context, key string) (int, error)
	// IncrExpire increments key and sets its TTL in one pipeline.
	IncrExpire(ctx context.Context, key string, expiration time.Duration) error
	// IncrBy adds delta and returns the new total. When the key was absent
	// (the new total equals delta) it sets expiration. delta must be positive.
	IncrBy(ctx context.Context, key string, delta int64, expiration time.Duration) (int64, error)
	// DecrBy subtracts delta. A rejected daily spend uses it to return the reserved cents.
	DecrBy(ctx context.Context, key string, delta int64) error
}

// chainLookup is the registered adapter and token list a withdrawal reads.
type chainLookup interface {
	Chain(id string) (types.Chain, error)
	TokensForChain(chainID string) []types.Token
}

// walletReader loads the wallet a withdrawal spends from.
type walletReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// transactionStore reads withdrawal rows and writes the idempotency key.
type transactionStore interface {
	FindByIdempotencyKey(ctx context.Context, key string) (*models.Transaction, error)
	SetIdempotencyKey(ctx context.Context, id uuid.UUID, key string) error
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
	List(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error)
	ListForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error)
}

// sweepRunner plans and executes a withdrawal. Consolidation, gas refresh,
// and limit reads stay on the sweep service.
type sweepRunner interface {
	PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int, toAddress string, callerAccountID uuid.UUID) (*sweep.Plan, error)
	ExecutePlan(ctx context.Context, plan *sweep.Plan, creds sweep.SigningCredentials, withdrawalTxID uuid.UUID, toAddress string, externalUserID string) (*sweep.Result, error)
}

type Service struct {
	registry        chainLookup
	webhookSvc      *webhook.Service
	mpc             mpcpkg.Service
	locker          Locker
	transactionRepo transactionStore
	walletRepo      walletReader
	addressRepo     *repositories.AddressRepository
	sweep           sweepRunner
	flags           accountGate
	dispatcher      refresh.Dispatcher
	usdQuote        USDQuote
	// createUsers, createTotp, createRows and createChains serve Create.
	// Request does not read them. A nil value fails Create before it persists.
	createUsers  UserLookup
	createTotp   TotpCheck
	createRows   WithdrawalRows
	createChains ChainCatalog
}

// Deps is everything the withdrawal service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Registry     chainLookup
	Webhook      *webhook.Service
	MPC          mpcpkg.Service
	Locker       Locker
	Transactions transactionStore
	Wallets      walletReader
	Addresses    *repositories.AddressRepository
	Sweep        sweepRunner
	Flags        accountGate
	Dispatcher   refresh.Dispatcher
}

// NewService wires the withdrawal service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		registry:        deps.Registry,
		webhookSvc:      deps.Webhook,
		mpc:             deps.MPC,
		locker:          deps.Locker,
		transactionRepo: deps.Transactions,
		walletRepo:      deps.Wallets,
		addressRepo:     deps.Addresses,
		sweep:           deps.Sweep,
		flags:           deps.Flags,
		dispatcher:      deps.Dispatcher,
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
	Passphrase      string    `json:"-"`
	IdempotencyKey  string    `json:"idempotency_key"`
	CallerAccountID uuid.UUID `json:"-"`
	// AccessTokenID, SpendingLimit and QuoteAmount carry a per-token daily USD
	// cap. A blank SpendingLimit keeps the withdrawal on today's path.
	AccessTokenID uuid.UUID `json:"-"`
	SpendingLimit string    `json:"-"`
	QuoteAmount   string    `json:"-"`
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
	defer mpcshare.DiscardPassphrase(&req.Passphrase)
	if len(req.Passphrase) < 12 {
		return nil, nil, ErrPassphraseTooShort
	}
	if err := s.gate(ctx, req.CallerAccountID); err != nil {
		return nil, nil, err
	}

	if req.IdempotencyKey != "" {
		existing, err := s.transactionRepo.FindByIdempotencyKey(ctx, req.IdempotencyKey)
		if err == nil && existing != nil {
			return existing, &Metadata{}, nil
		}
	}

	if s.locker == nil {
		return nil, nil, fmt.Errorf("redis lock: redis is not configured")
	}
	lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
	acquired, err := s.locker.SetNX(ctx, lockKey, "1", 60*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("redis lock: %w", err)
	}
	if !acquired {
		return nil, nil, ErrConcurrentWithdraw
	}
	defer s.locker.Del(ctx, lockKey)
	if err := s.enforceTokenSpendingLimit(ctx, req); err != nil {
		return nil, nil, err
	}

	walletPtr, err := s.walletRepo.FindByID(ctx, req.WalletID)
	if err != nil || walletPtr == nil {
		return nil, nil, fmt.Errorf("wallet not found")
	}
	wallet := *walletPtr
	walletAccount := uuid.Nil
	if wallet.AccountID != nil {
		walletAccount = *wallet.AccountID
	}
	if walletAccount != req.CallerAccountID {
		if err := s.gate(ctx, walletAccount); err != nil {
			return nil, nil, err
		}
	}

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
	defer mpcshare.DiscardPassphrase(&creds.Passphrase)
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
		if err := s.transactionRepo.SetIdempotencyKey(ctx, finalTx.ID, req.IdempotencyKey); err != nil {
			slog.Warn("failed to set idempotency_key on final withdrawal tx",
				"tx_id", finalTx.ID, "error", err)
		}
	}

	// The sweep executor already enqueues EventWithdrawalBroadcasting for the
	// final tx — do not re-emit here. The public withdrawal.broadcast event is
	// published by withdrawalevents.Publisher once the withdrawal row is
	// marked broadcast. The balance refresh goes through the Dispatcher port.
	s.dispatchBalanceRefresh(finalTx.WalletID.String(), wallet.Chain)

	slog.Info("withdrawal broadcast",
		"tx_id", finalTx.ID,
		"tx_hash", finalTx.TxHash,
		"chain", wallet.Chain,
		"strategy", plan.Strategy,
		"sweeps", len(result.Sweeps),
	)
	return finalTx, meta, nil
}

func (s *Service) dispatchBalanceRefresh(walletID, chainID string) {
	if s.dispatcher == nil {
		return
	}
	_ = s.dispatcher.DispatchBalances(walletID, chainID)
}

// decryptShareA decrypts the wallet's MPC customer share (share A) using the
// passphrase. On bad passphrase it records a failed attempt for rate-limiting
// and returns ErrInvalidPassphrase. Callers are responsible for zeroing the
// returned slice once they are done signing.
func (s *Service) gate(ctx context.Context, accountID uuid.UUID) error {
	if s.flags == nil || accountID == uuid.Nil {
		return nil
	}
	return s.flags(ctx, accountID)
}

func (s *Service) decryptShareA(ctx context.Context, wallet *models.Wallet, passphrase string) ([]byte, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
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
	count, err := s.locker.Int(ctx, key)
	if err != nil {
		// Without the counter the cap cannot be enforced, so refuse.
		return fmt.Errorf("passphrase attempt counter: %w", err)
	}
	if count >= 5 {
		return ErrTooManyAttempts
	}
	return nil
}

func (s *Service) recordFailedAttempt(ctx context.Context, walletID string) {
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", walletID)
	if err := s.locker.IncrExpire(ctx, key, 60*time.Second); err != nil {
		slog.Error("withdraw: record failed passphrase attempt", "wallet_id", walletID, "error", err)
	}
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
	tx, err := s.transactionRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, ErrTransactionNotFound
	}
	return tx, nil
}

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return s.transactionRepo.List(ctx, chainID, txType, status, userID, limit, offset)
}

// ListTransactionsForAccount is the account-scoped variant used by external API
// endpoints that accept a customer-supplied filter (e.g. external_user_id).
// Without this scoping, any API token could retrieve transactions for another
// account by guessing or enumerating external_ids.
func (s *Service) ListTransactionsForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return s.transactionRepo.ListForAccount(ctx, accountID, chainID, txType, status, userID, limit, offset)
}
