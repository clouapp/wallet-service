package withdraw

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memTransactions struct {
	rows []*models.Transaction
}

func newMemTransactions() *memTransactions { return &memTransactions{} }

func (m *memTransactions) add(tx *models.Transaction) {
	m.rows = append(m.rows, tx)
}

func (m *memTransactions) FindByIdempotencyKey(context.Context, string) (*models.Transaction, error) {
	return nil, fmt.Errorf("idempotency lookup is not used by these tests")
}

func (m *memTransactions) SetIdempotencyKey(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("idempotency write is not used by these tests")
}

func (m *memTransactions) FindByID(_ context.Context, id uuid.UUID) (*models.Transaction, error) {
	for _, tx := range m.rows {
		if tx.ID == id {
			return tx, nil
		}
	}
	return nil, fmt.Errorf("transaction not found")
}

func (m *memTransactions) List(_ context.Context, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	var matched []models.Transaction
	for _, tx := range m.rows {
		if chainID != "" && tx.Chain != chainID {
			continue
		}
		if txType != "" && tx.TxType != txType {
			continue
		}
		if status != "" && tx.Status != status {
			continue
		}
		if userID != "" && tx.ExternalUserID != userID {
			continue
		}
		matched = append(matched, *tx)
	}
	total := int64(len(matched))
	if offset > len(matched) {
		return []models.Transaction{}, total, nil
	}
	matched = matched[offset:]
	if limit < len(matched) {
		matched = matched[:limit]
	}
	return matched, total, nil
}

func (m *memTransactions) ListForAccount(ctx context.Context, _ uuid.UUID, chainID, txType, status, userID string, limit, offset int) ([]models.Transaction, int64, error) {
	return m.List(ctx, chainID, txType, status, userID, limit, offset)
}

type memWalletReader struct{}

func (memWalletReader) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, fmt.Errorf("wallet not found")
}
