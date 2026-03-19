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
	"github.com/goravel/framework/facades"
	"github.com/redis/go-redis/v9"

	"github.com/macromarkets/vault/app/models"
	chainpkg "github.com/macromarkets/vault/app/services/chain"
	mpcpkg "github.com/macromarkets/vault/app/services/mpc"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
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
	registry   *chainpkg.Registry
	webhookSvc *webhook.Service
	mpc        mpcpkg.Service
	secrets    *secretsmanager.Client
	rdb        *redis.Client
}

func NewService(
	registry *chainpkg.Registry,
	webhookSvc *webhook.Service,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
) *Service {
	return &Service{
		registry:   registry,
		webhookSvc: webhookSvc,
		mpc:        mpc,
		secrets:    secrets,
		rdb:        rdb,
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
	// Step 1: Validate passphrase length before any I/O
	if len(req.Passphrase) < 12 {
		return nil, ErrPassphraseTooShort
	}

	// Step 2: Idempotency check (no lock needed)
	if req.IdempotencyKey != "" {
		var existing models.Transaction
		if err := facades.Orm().Query().Where("idempotency_key", req.IdempotencyKey).First(&existing); err == nil {
			return &existing, nil
		}
	}

	// Step 3: Acquire per-wallet Redis lock
	lockKey := fmt.Sprintf("vault:lock:withdrawal:%s", req.WalletID)
	acquired, err := s.rdb.SetNX(ctx, lockKey, "1", 60*time.Second).Result()
	if err != nil {
		return nil, fmt.Errorf("redis lock: %w", err)
	}
	if !acquired {
		return nil, ErrConcurrentWithdraw
	}
	defer s.rdb.Del(ctx, lockKey)

	// Step 4: Load wallet and validate
	var wallet models.Wallet
	if err := facades.Orm().Query().Find(&wallet, req.WalletID); err != nil {
		return nil, fmt.Errorf("wallet not found: %w", err)
	}

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

	// Step 5: Check passphrase rate limit
	if err := s.checkRateLimit(ctx, req.WalletID.String()); err != nil {
		return nil, err
	}

	// Step 6: Check on-chain balance BEFORE loading key material
	bal, err := adapter.GetBalance(ctx, wallet.DepositAddress)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}
	if bal.Amount.Cmp(amount) < 0 {
		return nil, ErrInsufficientFunds
	}

	// Step 7: Decrypt share_A — hex-decode stored fields first
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

	// Step 8: Fetch share_B from Secrets Manager
	secret, err := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &wallet.MPCSecretARN,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch service share: %w", err)
	}
	shareB := secret.SecretBinary

	// Step 9: Defer zero-wipe IMMEDIATELY after shares loaded
	defer func() {
		for i := range shareA {
			shareA[i] = 0
		}
		for i := range shareB {
			shareB[i] = 0
		}
	}()

	// Step 10: Build unsigned transaction
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

	// Step 11: MPC Sign
	curve := mpcpkg.Curve(wallet.MPCCurve)
	sig, err := s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
		TxHashes: [][]byte{unsigned.RawBytes},
	})
	if err != nil {
		return nil, fmt.Errorf("mpc sign: %w", err)
	}

	// Step 12: Broadcast signed transaction
	signed := &types.SignedTx{
		ChainID:  wallet.Chain,
		RawBytes: sig,
	}
	txHash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		return nil, fmt.Errorf("broadcast: %w", err)
	}

	// Step 13: Persist transaction (no passphrase stored anywhere)
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
	if err := facades.Orm().Query().Create(tx); err != nil {
		return nil, fmt.Errorf("persist tx: %w", err)
	}

	// Step 14: Enqueue webhook notification (no key material)
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
		return nil // fail open on Redis error — don't block legitimate withdrawals
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
	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, id); err != nil {
		return nil, err
	}
	return &tx, nil
}

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, error) {
	query := facades.Orm().Query()

	if chainID != "" {
		query = query.Where("chain", chainID)
	}
	if txType != "" {
		query = query.Where("tx_type", txType)
	}
	if status != "" {
		query = query.Where("status", status)
	}
	if userID != "" {
		query = query.Where("external_user_id", userID)
	}
	if limit <= 0 {
		limit = 50
	}

	var txs []models.Transaction
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&txs); err != nil {
		return nil, err
	}
	return txs, nil
}
