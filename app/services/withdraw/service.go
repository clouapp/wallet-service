package withdraw

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
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
	"github.com/macrowallets/waas/pkg/types"
)

// Sentinel errors for HTTP response mapping in controller.
var (
	ErrInvalidPassphrase  = errors.New("invalid passphrase")
	ErrInsufficientFunds  = errors.New("insufficient funds")
	ErrConcurrentWithdraw = errors.New("withdrawal already in progress for this wallet")
	ErrPassphraseTooShort = errors.New("passphrase must be at least 12 characters")
	ErrTooManyAttempts    = errors.New("too many failed attempts, try again later")
)

// strategyLegacy is the pseudo-strategy returned in Metadata when the withdraw
// service takes the non-EVM legacy code path (sweep.PlanForWithdrawal reported
// ErrUnsupportedChain). It is not part of sweep.Strategy's enumerated values.
const strategyLegacy sweep.Strategy = "legacy"

type Service struct {
	registry        *chainpkg.Registry
	webhookSvc      *webhook.Service
	mpc             mpcpkg.Service
	secrets         *secretsmanager.Client
	rdb             *redis.Client
	transactionRepo repositories.TransactionRepository
	walletRepo      repositories.WalletRepository
	addressRepo     repositories.AddressRepository
	sweep           sweep.Service
}

func NewService(
	registry *chainpkg.Registry,
	webhookSvc *webhook.Service,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	transactionRepo repositories.TransactionRepository,
	walletRepo repositories.WalletRepository,
	addressRepo repositories.AddressRepository,
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
type WithdrawRequest struct {
	WalletID       uuid.UUID `json:"wallet_id"`
	ExternalUserID string    `json:"external_user_id"`
	ToAddress      string    `json:"to_address"`
	Amount         string    `json:"amount"`
	Asset          string    `json:"asset"`
	Passphrase     string    `json:"passphrase"`
	IdempotencyKey string    `json:"idempotency_key"`
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
// For v1 EVM chains the sweep service is authoritative for source selection.
// Non-EVM chains fall through to legacyWithdraw, which preserves the single
// base-address behavior that pre-dated the sweep refactor.
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

	lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
	acquired, err := s.rdb.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("redis lock: %w", err)
	}
	if !acquired {
		return nil, nil, ErrConcurrentWithdraw
	}
	defer s.rdb.Del(ctx, lockKey)

	walletPtr, err := s.walletRepo.FindByID(req.WalletID)
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

	plan, err := s.sweep.PlanForWithdrawal(ctx, wallet.ID, req.Asset, amount)
	if err != nil {
		if errors.Is(err, sweep.ErrUnsupportedChain) {
			return s.legacyWithdraw(ctx, req, &wallet, adapter, amount)
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
		if wallet.GasStatus != models.GasStatusSeeded {
			return nil, nil, sweep.ErrWalletNotGasReady
		}
	}

	shareA, err := s.decryptShareA(ctx, &wallet, req.Passphrase)
	if err != nil {
		return nil, nil, err
	}
	defer zeroShare(shareA)

	withdrawalTxID := uuid.New()
	result, err := s.sweep.ExecutePlan(ctx, plan, shareA, withdrawalTxID, req.ToAddress, req.ExternalUserID)
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
	// know the plan executed to completion.
	if req.IdempotencyKey != "" {
		finalTx.IdempotencyKey = req.IdempotencyKey
		if err := s.transactionRepo.UpdateFields(finalTx.ID, map[string]interface{}{
			"idempotency_key": req.IdempotencyKey,
		}); err != nil {
			slog.Warn("failed to set idempotency_key on final withdrawal tx",
				"tx_id", finalTx.ID, "error", err)
		}
	}

	// The sweep executor already enqueues EventWithdrawalBroadcast for the
	// final tx — do not re-emit here. We still dispatch the Goravel domain
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

// legacyWithdraw is the pre-sweep code path for chains the sweep service does
// not yet support (non-EVM in v1). It always uses wallet.DepositAddress as the
// source and preserves the original MPC / slip0010 signing branches.
//
// Returns Metadata with Strategy = strategyLegacy as a marker for callers.
func (s *Service) legacyWithdraw(
	ctx context.Context,
	req WithdrawRequest,
	wallet *models.Wallet,
	adapter types.Chain,
	amount *big.Int,
) (*models.Transaction, *Metadata, error) {
	if wallet.DepositAddress == nil {
		return nil, nil, fmt.Errorf("wallet has no deposit address")
	}
	fromAddress := wallet.DepositAddress

	bal, err := adapter.GetBalance(ctx, fromAddress.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("get balance: %w", err)
	}
	if bal.Amount.Cmp(amount) < 0 {
		return nil, nil, ErrInsufficientFunds
	}

	var tokenContract string
	var token *types.Token
	if req.Asset != adapter.NativeAsset() {
		t, err := s.registry.FindToken(wallet.Chain, req.Asset)
		if err != nil {
			return nil, nil, err
		}
		token = t
		tokenContract = t.Contract
	}

	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:   fromAddress.Address,
		To:     req.ToAddress,
		Amount: amount,
		Asset:  req.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build tx: %w", err)
	}

	var sig []byte

	if fromAddress.DerivationType == "slip0010" {
		sig, err = s.signWithChildKey(fromAddress, req.Passphrase, unsigned.RawBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("sign with child key: %w", err)
		}
	} else {
		shareA, decErr := s.decryptShareA(ctx, wallet, req.Passphrase)
		if decErr != nil {
			return nil, nil, decErr
		}
		defer zeroShare(shareA)

		secret, secretErr := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: &wallet.MPCSecretARN,
		})
		if secretErr != nil {
			return nil, nil, fmt.Errorf("fetch service share: %w", secretErr)
		}
		shareB := secret.SecretBinary
		defer zeroShare(shareB)

		curve := mpcpkg.Curve(wallet.MPCCurve)
		sig, err = s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
			TxHashes: [][]byte{unsigned.RawBytes},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("mpc sign: %w", err)
		}
	}

	signed := &types.SignedTx{
		ChainID:  wallet.Chain,
		RawBytes: sig,
	}
	txHash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		return nil, nil, fmt.Errorf("broadcast: %w", err)
	}

	tx := &models.Transaction{
		ID:             uuid.New(),
		WalletID:       wallet.ID,
		ExternalUserID: req.ExternalUserID,
		Chain:          wallet.Chain,
		TxType:         "withdrawal",
		TxHash:         txHash,
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		TokenContract:  tokenContract,
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Status:         string(types.TxStatusConfirming),
		IdempotencyKey: req.IdempotencyKey,
	}
	if err := s.transactionRepo.Create(tx); err != nil {
		return nil, nil, fmt.Errorf("persist tx: %w", err)
	}

	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalBroadcast, map[string]string{
		"tx_id": tx.ID.String(), "tx_hash": txHash,
	})

	_ = facades.Event().Job(&events.WithdrawalBroadcasted{}, []event.Arg{
		{Type: "string", Value: tx.WalletID.String()},
		{Type: "string", Value: wallet.Chain},
	}).Dispatch()

	slog.Info("withdrawal broadcast (legacy)",
		"tx_id", tx.ID, "tx_hash", txHash, "chain", wallet.Chain)
	return tx, &Metadata{Strategy: strategyLegacy}, nil
}

// decryptShareA decrypts the wallet's MPC customer share (share A) using the
// passphrase. On bad passphrase it records a failed attempt for rate-limiting
// and returns ErrInvalidPassphrase. Callers are responsible for zeroing the
// returned slice once they are done signing.
func (s *Service) decryptShareA(ctx context.Context, wallet *models.Wallet, passphrase string) ([]byte, error) {
	ciphertext, err := hex.DecodeString(wallet.MPCCustomerShare)
	if err != nil {
		return nil, fmt.Errorf("decode customer share: %w", err)
	}
	ivBytes, err := hex.DecodeString(wallet.MPCShareIV)
	if err != nil {
		return nil, fmt.Errorf("decode share iv: %w", err)
	}
	saltBytes, err := hex.DecodeString(wallet.MPCShareSalt)
	if err != nil {
		return nil, fmt.Errorf("decode share salt: %w", err)
	}

	enc := &mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: ivBytes, Salt: saltBytes}
	shareA, decErr := mpcpkg.DecryptShare(enc, passphrase)
	if decErr != nil {
		if errors.Is(decErr, mpcpkg.ErrInvalidPassphrase) {
			s.recordFailedAttempt(ctx, wallet.ID.String())
			return nil, ErrInvalidPassphrase
		}
		return nil, decErr
	}
	return shareA, nil
}

func (s *Service) signWithChildKey(addr *models.Address, passphrase string, txBytes []byte) ([]byte, error) {
	if addr.EncryptedPrivateKey == "" {
		return nil, fmt.Errorf("address has no encrypted private key")
	}

	ciphertext, err := hex.DecodeString(addr.EncryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted key: %w", err)
	}
	ivBytes, err := hex.DecodeString(addr.EncryptionIV)
	if err != nil {
		return nil, fmt.Errorf("decode iv: %w", err)
	}
	saltBytes, err := hex.DecodeString(addr.EncryptionSalt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	enc := &mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: ivBytes, Salt: saltBytes}
	childSeed, err := mpcpkg.DecryptShare(enc, passphrase)
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase")
	}
	defer zeroShare(childSeed)

	privKey := ed25519.NewKeyFromSeed(childSeed)
	defer zeroShare(privKey)

	return ed25519.Sign(privKey, txBytes), nil
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
