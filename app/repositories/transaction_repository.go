package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type TransactionRepository interface {
	Create(tx *models.Transaction) error
	FindByID(id uuid.UUID) (*models.Transaction, error)
	FindByIDAndWallet(txID string, walletID uuid.UUID) (*models.Transaction, error)
	FindByIdempotencyKey(key string) (*models.Transaction, error)
	FindByWallet(walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error)
	CountByChainAndTxHash(chainID, txHash, txType string) (int64, error)
	FindPendingByChain(chainID string) ([]models.Transaction, error)
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
	List(chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error)
}

type transactionRepository struct{}

func NewTransactionRepository() TransactionRepository {
	return &transactionRepository{}
}

func (r *transactionRepository) Create(tx *models.Transaction) error {
	return facades.Orm().Query().Create(tx)
}

func (r *transactionRepository) FindByID(id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, id); err != nil {
		return nil, err
	}
	if tx.ID == uuid.Nil {
		return nil, nil
	}
	return &tx, nil
}

func (r *transactionRepository) FindByIDAndWallet(txID string, walletID uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	err := facades.Orm().Query().
		Where("id = ? AND wallet_id = ?", txID, walletID).
		First(&tx)
	if err != nil {
		return nil, err
	}
	if tx.ID == uuid.Nil {
		return nil, nil
	}
	return &tx, nil
}

func (r *transactionRepository) FindByIdempotencyKey(key string) (*models.Transaction, error) {
	var tx models.Transaction
	err := facades.Orm().Query().Where("idempotency_key", key).First(&tx)
	if err != nil {
		return nil, err
	}
	if tx.ID == uuid.Nil {
		return nil, nil
	}
	return &tx, nil
}

func (r *transactionRepository) FindByWallet(walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	countQuery := facades.Orm().Query().
		Model(&models.Transaction{}).
		Where("wallet_id = ?", walletID)
	if txType != "" {
		countQuery = countQuery.Where("tx_type = ?", txType)
	}
	if status != "" {
		countQuery = countQuery.Where("status = ?", status)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, err
	}

	dataQuery := facades.Orm().Query().
		Where("wallet_id = ?", walletID)
	if txType != "" {
		dataQuery = dataQuery.Where("tx_type = ?", txType)
	}
	if status != "" {
		dataQuery = dataQuery.Where("status = ?", status)
	}

	var transactions []models.Transaction
	err = dataQuery.Offset(offset).Limit(limit).Find(&transactions)
	return transactions, total, err
}

func (r *transactionRepository) CountByChainAndTxHash(chainID, txHash, txType string) (int64, error) {
	return facades.Orm().Query().
		Model(&models.Transaction{}).
		Where("chain", chainID).
		Where("tx_hash", txHash).
		Where("tx_type", txType).
		Count()
}

func (r *transactionRepository) FindPendingByChain(chainID string) ([]models.Transaction, error) {
	var pending []models.Transaction
	err := facades.Orm().Query().
		Where("chain", chainID).
		Where("tx_type", "deposit").
		WhereIn("status", []interface{}{"pending", "confirming"}).
		Find(&pending)
	return pending, err
}

func (r *transactionRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	_, err := facades.Orm().Query().
		Model(&models.Transaction{}).
		Where("id", id).
		Update(fields)
	return err
}

func (r *transactionRepository) List(chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}

	countQuery := facades.Orm().Query().Model(&models.Transaction{})
	dataQuery := facades.Orm().Query()
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

	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, err
	}

	var txs []models.Transaction
	err = dataQuery.Order("created_at DESC").Limit(limit).Offset(offset).Find(&txs)
	return txs, total, err
}
