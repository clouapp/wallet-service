package withdraw

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
)

type Service struct {
	db         *sqlx.DB
	registry   *chain.Registry
	sqs        queue.Sender
	webhookSvc *webhook.Service
}

func NewService(db *sqlx.DB, registry *chain.Registry, sqs queue.Sender, webhookSvc *webhook.Service) *Service {
	return &Service{db: db, registry: registry, sqs: sqs, webhookSvc: webhookSvc}
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
		if err := s.db.GetContext(ctx, &existing, "SELECT * FROM transactions WHERE idempotency_key = $1", req.IdempotencyKey); err == nil {
			return &existing, nil
		}
	}

	// 2. Get wallet
	var wallet models.Wallet
	if err := s.db.GetContext(ctx, &wallet, "SELECT * FROM wallets WHERE id = $1", req.WalletID); err != nil {
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

	if _, err := s.db.NamedExecContext(ctx, `
		INSERT INTO transactions (id, wallet_id, external_user_id, chain, tx_type, to_address,
			amount, asset, token_contract, required_confs, status, idempotency_key, created_at, updated_at)
		VALUES (:id, :wallet_id, :external_user_id, :chain, :tx_type, :to_address,
			:amount, :asset, :token_contract, :required_confs, :status, :idempotency_key, NOW(), NOW())`, tx); err != nil {
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
	if err := s.db.GetContext(ctx, &tx, "SELECT * FROM transactions WHERE id = $1", txID); err != nil {
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
	s.db.ExecContext(ctx, `UPDATE transactions SET tx_hash = $1, status = 'confirming' WHERE id = $2`, txHash, txID)
	s.webhookSvc.EnqueueEvent(ctx, txID, types.EventWithdrawalBroadcast, map[string]string{
		"tx_id": txID.String(), "tx_hash": txHash,
	})

	slog.Info("withdrawal broadcast", "tx_id", txID, "tx_hash", txHash, "chain", msg.ChainID)
	return nil
}

func (s *Service) failTx(ctx context.Context, txID uuid.UUID, reason string) {
	slog.Error("withdrawal failed", "tx_id", txID, "reason", reason)
	s.db.ExecContext(ctx, `UPDATE transactions SET status = 'failed', error_message = $1 WHERE id = $2`, reason, txID)
	s.webhookSvc.EnqueueEvent(ctx, txID, types.EventWithdrawalFailed, map[string]string{
		"tx_id": txID.String(), "reason": reason,
	})
}

// ---------------------------------------------------------------------------
// Query helpers (used by API controllers)
// ---------------------------------------------------------------------------

func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	return &tx, s.db.GetContext(ctx, &tx, "SELECT * FROM transactions WHERE id = $1", id)
}

func (s *Service) ListTransactions(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, error) {
	query := "SELECT * FROM transactions WHERE 1=1"
	args := []interface{}{}
	idx := 1

	if chainID != "" {
		query += fmt.Sprintf(" AND chain = $%d", idx)
		args = append(args, chainID)
		idx++
	}
	if txType != "" {
		query += fmt.Sprintf(" AND tx_type = $%d", idx)
		args = append(args, txType)
		idx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, status)
		idx++
	}
	if userID != "" {
		query += fmt.Sprintf(" AND external_user_id = $%d", idx)
		args = append(args, userID)
		idx++
	}
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	var txs []models.Transaction
	return txs, s.db.SelectContext(ctx, &txs, query, args...)
}
