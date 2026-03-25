package withdraw

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
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

type Service struct {
	registry        *chainpkg.Registry
	webhookSvc      *webhook.Service
	mpc             mpcpkg.Service
	secrets         *secretsmanager.Client
	rdb             *redis.Client
	transactionRepo repositories.TransactionRepository
	walletRepo      repositories.WalletRepository
}

func NewService(
	registry *chainpkg.Registry,
	webhookSvc *webhook.Service,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	transactionRepo repositories.TransactionRepository,
	walletRepo repositories.WalletRepository,
) *Service {
	return &Service{
		registry:        registry,
		webhookSvc:      webhookSvc,
		mpc:             mpc,
		secrets:         secrets,
		rdb:             rdb,
		transactionRepo: transactionRepo,
		walletRepo:      walletRepo,
	}
}

type WithdrawRequest struct {
	WalletID       uuid.UUID `json:"wallet_id"`
	ExternalUserID string    `json:"external_user_id"`
	ToAddress      string    `json:"to_address"`
	Amount         string    `json:"amount"`
	Asset          string    `json:"asset"`
	Passphrase     string    `json:"passphrase"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func (s *Service) Request(ctx context.Context, req WithdrawRequest) (*models.Transaction, error) {
	if len(req.Passphrase) < 12 {
		return nil, ErrPassphraseTooShort
	}

	if req.IdempotencyKey != "" {
		existing, err := s.transactionRepo.FindByIdempotencyKey(req.IdempotencyKey)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
	acquired, err := s.rdb.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
	if err != nil {
		return nil, fmt.Errorf("redis lock: %w", err)
	}
	if !acquired {
		return nil, ErrConcurrentWithdraw
	}
	defer s.rdb.Del(ctx, lockKey)

	walletPtr, err := s.walletRepo.FindByID(req.WalletID)
	if err != nil || walletPtr == nil {
		return nil, fmt.Errorf("wallet not found")
	}
	wallet := *walletPtr

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, err
	}
	if !adapter.ValidateAddress(req.ToAddress) {
		return nil, fmt.Errorf("invalid address for chain %s", wallet.Chain)
	}

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount: %s", req.Amount)
	}

	if err := s.checkRateLimit(ctx, req.WalletID.String()); err != nil {
		return nil, err
	}

	bal, err := adapter.GetBalance(ctx, wallet.DepositAddress)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}
	if bal.Amount.Cmp(amount) < 0 {
		return nil, ErrInsufficientFunds
	}

	ciphertext, err := hex.DecodeString(wallet.MPCCustomerShare)
	if err != nil {
		return nil, fmt.Errorf("decode customer share: %w", err)
	}
	iv, err := hex.DecodeString(wallet.MPCShareIV)
	if err != nil {
		return nil, fmt.Errorf("decode share iv: %w", err)
	}
	salt, err := hex.DecodeString(wallet.MPCShareSalt)
	if err != nil {
		return nil, fmt.Errorf("decode share salt: %w", err)
	}

	enc := &mpcpkg.EncryptedShare{
		Ciphertext: ciphertext,
		IV:         iv,
		Salt:       salt,
	}
	shareA, err := mpcpkg.DecryptShare(enc, req.Passphrase)
	if err != nil {
		if errors.Is(err, mpcpkg.ErrInvalidPassphrase) {
			s.recordFailedAttempt(ctx, req.WalletID.String())
			return nil, ErrInvalidPassphrase
		}
		return nil, err
	}

	secret, err := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &wallet.MPCSecretARN,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch service share: %w", err)
	}
	shareB := secret.SecretBinary

	defer func() {
		for i := range shareA {
			shareA[i] = 0
		}
		for i := range shareB {
			shareB[i] = 0
		}
	}()

	var tokenContract string
	var token *types.Token
	if req.Asset != adapter.NativeAsset() {
		t, err := s.registry.FindToken(wallet.Chain, req.Asset)
		if err != nil {
			return nil, err
		}
		token = t
		tokenContract = t.Contract
	}

	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From:   wallet.DepositAddress,
		To:     req.ToAddress,
		Amount: amount,
		Asset:  req.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, fmt.Errorf("build tx: %w", err)
	}

	curve := mpcpkg.Curve(wallet.MPCCurve)
	sig, err := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
		TxHashes: [][]byte{unsigned.RawBytes},
	})
	if err != nil {
		return nil, fmt.Errorf("mpc sign: %w", err)
	}

	signed := &types.SignedTx{
		ChainID:  wallet.Chain,
		RawBytes: sig,
	}
	txHash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		return nil, fmt.Errorf("broadcast: %w", err)
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
		return nil, fmt.Errorf("persist tx: %w", err)
	}

	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalBroadcast, map[string]string{
		"tx_id": tx.ID.String(), "tx_hash": txHash,
	})

	slog.Info("withdrawal broadcast", "tx_id", tx.ID, "tx_hash", txHash, "chain", wallet.Chain)
	return tx, nil
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

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, error) {
	return s.transactionRepo.List(chainID, txType, status, userID, limit, offset)
}
