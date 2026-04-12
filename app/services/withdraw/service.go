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
	addressRepo     repositories.AddressRepository
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
	}
}

type WithdrawRequest struct {
	WalletID       uuid.UUID  `json:"wallet_id"`
	FromAddressID  *uuid.UUID `json:"from_address_id"`
	ExternalUserID string     `json:"external_user_id"`
	ToAddress      string     `json:"to_address"`
	Amount         string     `json:"amount"`
	Asset          string     `json:"asset"`
	Passphrase     string     `json:"passphrase"`
	IdempotencyKey string     `json:"idempotency_key"`
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

	var fromAddress *models.Address
	if req.FromAddressID != nil {
		fromAddress, err = s.addressRepo.FindByID(*req.FromAddressID)
		if err != nil || fromAddress == nil {
			return nil, fmt.Errorf("from address not found")
		}
		if fromAddress.WalletID != wallet.ID {
			return nil, fmt.Errorf("address does not belong to this wallet")
		}
	} else {
		if wallet.DepositAddress == nil {
			return nil, fmt.Errorf("wallet has no deposit address")
		}
		fromAddress = wallet.DepositAddress
	}

	bal, err := adapter.GetBalance(ctx, fromAddress.Address)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}
	if bal.Amount.Cmp(amount) < 0 {
		return nil, ErrInsufficientFunds
	}

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
		From:   fromAddress.Address,
		To:     req.ToAddress,
		Amount: amount,
		Asset:  req.Asset,
		Token:  token,
	})
	if err != nil {
		return nil, fmt.Errorf("build tx: %w", err)
	}

	var sig []byte

	if fromAddress.DerivationType == "slip0010" {
		sig, err = s.signWithChildKey(fromAddress, req.Passphrase, unsigned.RawBytes)
		if err != nil {
			return nil, fmt.Errorf("sign with child key: %w", err)
		}
	} else {
		ciphertext, decErr := hex.DecodeString(wallet.MPCCustomerShare)
		if decErr != nil {
			return nil, fmt.Errorf("decode customer share: %w", decErr)
		}
		ivBytes, decErr := hex.DecodeString(wallet.MPCShareIV)
		if decErr != nil {
			return nil, fmt.Errorf("decode share iv: %w", decErr)
		}
		saltBytes, decErr := hex.DecodeString(wallet.MPCShareSalt)
		if decErr != nil {
			return nil, fmt.Errorf("decode share salt: %w", decErr)
		}

		enc := &mpcpkg.EncryptedShare{Ciphertext: ciphertext, IV: ivBytes, Salt: saltBytes}
		shareA, decErr := mpcpkg.DecryptShare(enc, req.Passphrase)
		if decErr != nil {
			if errors.Is(decErr, mpcpkg.ErrInvalidPassphrase) {
				s.recordFailedAttempt(ctx, req.WalletID.String())
				return nil, ErrInvalidPassphrase
			}
			return nil, decErr
		}
		defer func() {
			for i := range shareA {
				shareA[i] = 0
			}
		}()

		secret, secretErr := s.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: &wallet.MPCSecretARN,
		})
		if secretErr != nil {
			return nil, fmt.Errorf("fetch service share: %w", secretErr)
		}
		shareB := secret.SecretBinary
		defer func() {
			for i := range shareB {
				shareB[i] = 0
			}
		}()

		curve := mpcpkg.Curve(wallet.MPCCurve)
		sig, err = s.mpc.Sign(ctx, curve, shareA, shareB, mpcpkg.SignInputs{
			TxHashes: [][]byte{unsigned.RawBytes},
		})
		if err != nil {
			return nil, fmt.Errorf("mpc sign: %w", err)
		}
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

	_ = facades.Event().Job(&events.WithdrawalBroadcasted{}, []event.Arg{
		{Type: "string", Value: tx.WalletID.String()},
		{Type: "string", Value: wallet.Chain},
	}).Dispatch()

	slog.Info("withdrawal broadcast", "tx_id", tx.ID, "tx_hash", txHash, "chain", wallet.Chain)
	return tx, nil
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
	defer func() {
		for i := range childSeed {
			childSeed[i] = 0
		}
	}()

	privKey := ed25519.NewKeyFromSeed(childSeed)
	defer func() {
		for i := range privKey {
			privKey[i] = 0
		}
	}()

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
