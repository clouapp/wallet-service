package withdraw

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
)

type Service struct {
	registry   *chain.Registry
	webhookSvc *webhook.Service
}

func NewService(registry *chain.Registry, webhookSvc *webhook.Service) *Service {
	return &Service{registry: registry, webhookSvc: webhookSvc}
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

	// 7. TODO: MPC synchronous signing (replacing async SQS worker path)

	return tx, nil
}

// ---------------------------------------------------------------------------
// Execute removed — replaced by synchronous MPC signing in API Lambda (Task 10)
// ---------------------------------------------------------------------------

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
