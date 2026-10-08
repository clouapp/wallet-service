package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// TransactionRepository loads and updates transaction rows.
type TransactionRepository struct {
	db.Base
}

// NewTransactionRepository builds a repository. Nil uses a fresh query per call.
func NewTransactionRepository(query orm.Query) *TransactionRepository {
	return &TransactionRepository{Base: db.NewBase(query)}
}

// Within runs fn inside one transaction. Queries made with the callback
// context join that transaction, including a webhook event for the same sweep leg.
func (r *TransactionRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// Create inserts a transaction after rejecting a negative amount or fee.
func (r *TransactionRepository) Create(ctx context.Context, tx *models.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if err := tx.ValidateAmounts(); err != nil {
		return err
	}
	if err := r.Query(ctx).Create(tx); err != nil {
		return fmt.Errorf("create transaction: %w", err)
	}
	return nil
}

// FindByID returns one transaction.
func (r *TransactionRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Find(&tx, id); err != nil {
		return nil, fmt.Errorf("find transaction: %w", err)
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// FindByIDForAccount returns the transaction when it belongs to a wallet of
// accountID. A missing row and a transaction on another account are both
// ErrRepositoryNotFound.
func (r *TransactionRepository) FindByIDForAccount(ctx context.Context, id, accountID uuid.UUID) (*models.Transaction, error) {
	if id == uuid.Nil || accountID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	var tx models.Transaction
	err := r.Query(ctx).
		Where("id = ? AND wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", id, accountID).
		First(&tx)
	if err != nil {
		return nil, db.NotFound(err, "find transaction")
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// FindByIDAndWallet returns the transaction when it belongs to walletID.
func (r *TransactionRepository) FindByIDAndWallet(ctx context.Context, txID string, walletID uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("id = ? AND wallet_id = ?", txID, walletID).First(&tx); err != nil {
		return nil, fmt.Errorf("find transaction: %w", err)
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// FindByIdempotencyKey returns the transaction stored under a withdrawal idempotency key.
func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("idempotency_key", key).First(&tx); err != nil {
		return nil, fmt.Errorf("find transaction by idempotency key: %w", err)
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// FindByWallet lists transactions for a wallet, optionally filtered by type and status.
func (r *TransactionRepository) FindByWallet(ctx context.Context, walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where("wallet_id = ?", walletID)
	dataQuery := r.Query(ctx).Where("wallet_id = ?", walletID)
	if txType != "" {
		countQuery = countQuery.Where("tx_type = ?", txType)
		dataQuery = dataQuery.Where("tx_type = ?", txType)
	}
	if status != "" {
		countQuery = countQuery.Where("status = ?", status)
		dataQuery = dataQuery.Where("status = ?", status)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	var transactions []models.Transaction
	if err := dataQuery.Offset(offset).Limit(limit).Find(&transactions); err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	return transactions, total, nil
}

// FindByChainAndTxHash returns the transaction for a chain and hash.
func (r *TransactionRepository) FindByChainAndTxHash(ctx context.Context, chainID, txHash string) (*models.Transaction, error) {
	var tx models.Transaction
	if err := r.Query(ctx).Where("chain = ? AND tx_hash = ?", chainID, txHash).First(&tx); err != nil {
		return nil, fmt.Errorf("find transaction by hash: %w", err)
	}
	if tx.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &tx, nil
}

// CountByChainAndTxHash counts rows for a chain, hash, and type.
func (r *TransactionRepository) CountByChainAndTxHash(ctx context.Context, chainID, txHash, txType string) (int64, error) {
	count, err := r.Query(ctx).Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("tx_type", txType).
		Count()
	if err != nil {
		return 0, fmt.Errorf("count transactions by hash: %w", err)
	}
	return count, nil
}

// CountByChainTxHashAndLogIndex counts rows for a chain, hash, log index, and type.
func (r *TransactionRepository) CountByChainTxHashAndLogIndex(ctx context.Context, chainID, txHash string, logIndex int, txType string) (int64, error) {
	count, err := r.Query(ctx).Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("log_index", logIndex).
		Where("tx_type", txType).
		Count()
	if err != nil {
		return 0, fmt.Errorf("count transactions by log index: %w", err)
	}
	return count, nil
}

// internalTransferTxTypes move funds between addresses of one wallet: a sweep
// (child to base) and the gas seed that pays for it (base to child). Withdrawals are not
// listed: one that reaches a watched address is a real deposit for its owner.
var internalTransferTxTypes = []string{models.TxTypeSweep, models.TxTypeGasSeed}

// CountInternalTransfers counts the sweeps and gas seeds the wallet recorded for this
// transaction, so the deposit scanners do not record the same move again as a deposit.
func (r *TransactionRepository) CountInternalTransfers(ctx context.Context, chainID, txHash string, walletID uuid.UUID) (int64, error) {
	if chainID == "" || txHash == "" || walletID == uuid.Nil {
		return 0, fmt.Errorf("chain, transaction hash and wallet are required to look up internal transfers")
	}
	count, err := r.Query(ctx).
		Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("wallet_id", walletID).
		WhereIn("tx_type", []any{models.TxTypeSweep, models.TxTypeGasSeed}).
		Count()
	if err != nil {
		return 0, fmt.Errorf("count internal transfers: %w", err)
	}
	return count, nil
}

// FindPendingByChain returns every transaction on chainID that the confirmation
// loop must advance — deposits plus outbound legs (withdrawals, sweeps, gas seeds).
func (r *TransactionRepository) FindPendingByChain(ctx context.Context, chainID string) ([]models.Transaction, error) {
	var pending []models.Transaction
	err := r.Query(ctx).
		Where("chain", chainID).
		WhereIn("tx_type", []any{
			models.TxTypeDeposit,
			models.TxTypeWithdrawal,
			models.TxTypeSweep,
			models.TxTypeGasSeed,
		}).
		WhereIn("status", []any{string(types.TxStatusPending), string(types.TxStatusConfirming)}).
		Find(&pending)
	if err != nil {
		return nil, fmt.Errorf("list pending transactions: %w", err)
	}
	return pending, nil
}

// FindConfirmedOutboundWithoutFee lists confirmed withdrawals, sweeps and gas seeds
// of chainID whose paid fee was never recorded, oldest first.
func (r *TransactionRepository) FindConfirmedOutboundWithoutFee(ctx context.Context, chainID string, limit int) ([]models.Transaction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list transactions without fee: limit is required")
	}
	var rows []models.Transaction
	err := r.Query(ctx).
		Where("chain", chainID).
		WhereIn("tx_type", []any{models.TxTypeWithdrawal, models.TxTypeSweep, models.TxTypeGasSeed}).
		Where("status", string(types.TxStatusConfirmed)).
		Where("tx_hash <> ''").
		Where("(fee IS NULL OR fee = '')").
		Order("created_at").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("list transactions without fee: %w", err)
	}
	return rows, nil
}

// SetFee stores the paid fee in native base units.
func (r *TransactionRepository) SetFee(ctx context.Context, id uuid.UUID, fee string) error {
	return r.updateColumns(ctx, id, map[string]any{"fee": fee}, "set transaction fee")
}

// SetBlockNumber sets transactions.block_number.
func (r *TransactionRepository) SetBlockNumber(ctx context.Context, id uuid.UUID, block uint64) error {
	return r.updateColumns(ctx, id, map[string]any{"block_number": block}, "set transaction block number")
}

// RecordConfirmations stores the confirmation count, status, and confirmed_at together.
func (r *TransactionRepository) RecordConfirmations(ctx context.Context, id uuid.UUID, confirmations int, status string, confirmedAt *time.Time) error {
	return r.updateColumns(ctx, id, map[string]any{
		"confirmations": confirmations,
		"status":        status,
		"confirmed_at":  confirmedAt,
	}, "record transaction confirmations")
}

// SetConfirmations sets transactions.confirmations.
func (r *TransactionRepository) SetConfirmations(ctx context.Context, id uuid.UUID, confirmations int) error {
	return r.updateColumns(ctx, id, map[string]any{"confirmations": confirmations}, "set transaction confirmations")
}

// SetAmount sets transactions.amount.
func (r *TransactionRepository) SetAmount(ctx context.Context, id uuid.UUID, value string) error {
	return r.updateColumns(ctx, id, map[string]any{"amount": value}, "set transaction amount")
}

// SetIdempotencyKey sets transactions.idempotency_key.
func (r *TransactionRepository) SetIdempotencyKey(ctx context.Context, id uuid.UUID, key string) error {
	return r.updateColumns(ctx, id, map[string]any{"idempotency_key": key}, "set transaction idempotency key")
}

// List returns transactions matching the optional filters, newest first.
func (r *TransactionRepository) List(ctx context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	countQuery := r.Query(ctx).Model(&models.Transaction{})
	dataQuery := r.Query(ctx)
	countQuery, dataQuery = applyTransactionFilters(countQuery, dataQuery, chainID, txType, status, userID)
	return listTransactions(countQuery, dataQuery, limit, offset, "created_at DESC")
}

// ListForAccount is the account-scoped variant of List used by the external API.
func (r *TransactionRepository) ListForAccount(ctx context.Context, accountID uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	scope := "wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)"
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where(scope, accountID)
	dataQuery := r.Query(ctx).Where(scope, accountID)
	countQuery, dataQuery = applyTransactionFilters(countQuery, dataQuery, chainID, txType, status, userID)
	return listTransactions(countQuery, dataQuery, limit, offset, "created_at DESC")
}

// ListByWalletAndChain lists one wallet's transactions on a chain, newest block first.
func (r *TransactionRepository) ListByWalletAndChain(ctx context.Context, walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	countQuery := r.Query(ctx).Model(&models.Transaction{}).Where("wallet_id = ?", walletID).Where("chain = ?", chainID)
	dataQuery := r.Query(ctx).Where("wallet_id = ?", walletID).Where("chain = ?", chainID)
	return listTransactions(countQuery, dataQuery, limit, offset, "block_number DESC, created_at DESC")
}

func (r *TransactionRepository) updateColumns(ctx context.Context, id uuid.UUID, columns map[string]any, op string) error {
	if id == uuid.Nil {
		return fmt.Errorf("transaction id is required")
	}
	if err := amount.RequireNonNegativeColumns(columns, models.TransactionAmountColumns...); err != nil {
		return err
	}
	if _, err := r.Query(ctx).Model(&models.Transaction{}).Where("id", id).Update(columns); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func applyTransactionFilters(countQuery, dataQuery orm.Query, chainID, txType, status, userID string) (orm.Query, orm.Query) {
	if chainID != "" {
		countQuery = countQuery.Where("chain", chainID)
		dataQuery = dataQuery.Where("chain", chainID)
	}
	if txType != "" {
		countQuery = countQuery.Where("tx_type", txType)
		dataQuery = dataQuery.Where("tx_type", txType)
	}
	if status != "" {
		countQuery = countQuery.Where("status", status)
		dataQuery = dataQuery.Where("status", status)
	}
	if userID != "" {
		countQuery = countQuery.Where("external_user_id", userID)
		dataQuery = dataQuery.Where("external_user_id", userID)
	}
	return countQuery, dataQuery
}

func listTransactions(countQuery, dataQuery orm.Query, limit, offset int, order string) ([]models.Transaction, int64, error) {
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	var txs []models.Transaction
	if err := dataQuery.Order(order).Limit(limit).Offset(offset).Find(&txs); err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	return txs, total, nil
}
