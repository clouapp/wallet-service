package withdraw

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
)

type Service struct {
	registry   *chain.Registry
	sqs        queue.Sender
	webhookSvc *webhook.Service
}

func NewService(registry *chain.Registry, sqs queue.Sender, webhookSvc *webhook.Service) *Service {
	return &Service{registry: registry, sqs: sqs, webhookSvc: webhookSvc}
}

// ---------------------------------------------------------------------------
// Request creates the withdrawal record and enqueues to SQS.
// Called by the API Lambda. Actual signing/broadcast happens in the worker.
// ---------------------------------------------------------------------------

type WithdrawRequest struct {
	WalletID       uuid.UUID `json:"wallet_id"`
	ExternalUserID string    `json:"external_user_id"`
	ToAddress      string    `json:"to_address"`
	Amount         string    `json:"amount"`
	Asset          string    `json:"asset"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func (s *Service) Request(ctx context.Context, req WithdrawRequest) (*models.Transaction, error) {
	// 1. Idempotency
	if req.IdempotencyKey != "" {
		var existing models.Transaction
		if err := facades.Orm().Query().Where("idempotency_key", req.IdempotencyKey).First(&existing); err == nil {
			return &existing, nil
		}
	}

	// 2. Get wallet
	var wallet models.Wallet
	if err := facades.Orm().Query().Find(&wallet, req.WalletID); err != nil {
		return nil, fmt.Errorf("wallet not found: %w", err)
	}

	// 3. Validate chain + address
	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return nil, err
	}
	if !adapter.ValidateAddress(req.ToAddress) {
		return nil, fmt.Errorf("invalid address for chain %s", wallet.Chain)
	}

	// 4. Resolve token contract
	var tokenContract string
	if req.Asset != adapter.NativeAsset() {
		t, err := s.registry.FindToken(wallet.Chain, req.Asset)
		if err != nil {
			return nil, err
		}
		tokenContract = t.Contract
	}

	// 5. Create transaction record
	tx := &models.Transaction{
		ID:             uuid.New(),
		WalletID:       wallet.ID,
		ExternalUserID: req.ExternalUserID,
		Chain:          wallet.Chain,
		TxType:         "withdrawal",
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		TokenContract:  tokenContract,
		RequiredConfs:  int(adapter.RequiredConfirmations()),
		Status:         string(types.TxStatusPending),
		IdempotencyKey: req.IdempotencyKey,
		// CreatedAt and UpdatedAt handled by orm.Model
	}

	if err := facades.Orm().Query().Create(tx); err != nil {
		return nil, fmt.Errorf("insert tx: %w", err)
	}

	// 6. Fire webhook: pending
	s.webhookSvc.EnqueueEvent(ctx, tx.ID, types.EventWithdrawalPending, tx)

	// 7. Enqueue to SQS for async processing
	sqsMsg := types.WithdrawalMessage{
		TransactionID:  tx.ID.String(),
		WalletID:       wallet.ID.String(),
		ChainID:        wallet.Chain,
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		ExternalUserID: req.ExternalUserID,
	}
	if tokenContract != "" {
		sqsMsg.TokenContract = tokenContract
	}
	if err := s.sqs.SendWithdrawal(ctx, sqsMsg); err != nil {
		// SQS send failed — mark tx as failed
		s.failTx(ctx, tx.ID, fmt.Sprintf("queue send: %v", err))
		return nil, fmt.Errorf("enqueue withdrawal: %w", err)
	}

	return tx, nil
}

// ---------------------------------------------------------------------------
// Execute is called by the SQS Lambda worker. Does the actual signing + broadcast.
// ---------------------------------------------------------------------------

func (s *Service) Execute(ctx context.Context, msg types.WithdrawalMessage) error {
	txID, _ := uuid.Parse(msg.TransactionID)

	// Check tx still pending (idempotency for SQS redelivery)
	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, txID); err != nil {
		return fmt.Errorf("tx not found: %w", err)
	}
	if tx.Status != string(types.TxStatusPending) {
		slog.Info("tx already processed, skipping", "tx_id", txID, "status", tx.Status)
		return nil
	}

	adapter, err := s.registry.Chain(msg.ChainID)
	if err != nil {
		s.failTx(ctx, txID, err.Error())
		return err
	}

	amount, ok := new(big.Int).SetString(msg.Amount, 10)
	if !ok {
		s.failTx(ctx, txID, "invalid amount")
		return fmt.Errorf("invalid amount: %s", msg.Amount)
	}

	// Resolve token
	var token *types.Token
	if msg.TokenContract != "" {
		t, err := s.registry.FindToken(msg.ChainID, msg.Asset)
		if err != nil {
			s.failTx(ctx, txID, err.Error())
			return err
		}
		token = t
	}

	// Build TX
	fromAddress := "hot-wallet-address" // TODO: resolve from wallet
	unsigned, err := adapter.BuildTransfer(ctx, types.TransferRequest{
		From: fromAddress, To: msg.ToAddress, Amount: amount, Asset: msg.Asset, Token: token,
	})
	if err != nil {
		s.failTx(ctx, txID, fmt.Sprintf("build: %v", err))
		return err
	}

	// Sign
	privateKey := []byte("placeholder") // TODO: KMS
	signed, err := adapter.SignTransaction(ctx, unsigned, privateKey)
	if err != nil {
		s.failTx(ctx, txID, fmt.Sprintf("sign: %v", err))
		return err
	}
	s.webhookSvc.EnqueueEvent(ctx, txID, types.EventWithdrawalSigned, tx)

	// Broadcast
	txHash, err := adapter.BroadcastTransaction(ctx, signed)
	if err != nil {
		s.failTx(ctx, txID, fmt.Sprintf("broadcast: %v", err))
		return err
	}

	// Update tx with hash
	facades.Orm().Query().Model(&models.Transaction{}).Where("id", txID).Update(map[string]interface{}{
		"tx_hash": txHash,
		"status":  "confirming",
	})
	s.webhookSvc.EnqueueEvent(ctx, txID, types.EventWithdrawalBroadcast, map[string]string{
		"tx_id": txID.String(), "tx_hash": txHash,
	})

	slog.Info("withdrawal broadcast", "tx_id", txID, "tx_hash", txHash, "chain", msg.ChainID)
	return nil
}

func (s *Service) failTx(ctx context.Context, txID uuid.UUID, reason string) {
	slog.Error("withdrawal failed", "tx_id", txID, "reason", reason)
	facades.Orm().Query().Model(&models.Transaction{}).Where("id", txID).Update(map[string]interface{}{
		"status":        "failed",
		"error_message": reason,
	})
	s.webhookSvc.EnqueueEvent(ctx, txID, types.EventWithdrawalFailed, map[string]string{
		"tx_id": txID.String(), "reason": reason,
	})
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
