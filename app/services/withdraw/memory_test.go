package withdraw

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memTransactions struct {
	rows []*models.Transaction
	// wallets maps a wallet to the account that owns it.
	wallets map[uuid.UUID]uuid.UUID
	// err, when set, fails FindByIDForAccount.
	err error
	// byKey answers FindByIdempotencyKey; a missing key is a lookup error.
	byKey map[string]*models.Transaction
	// lastChangesCall is the last ListChangedForAccount call.
	lastChangesCall changesCall
}

// changesCall records the arguments of one ListChangedForAccount call.
type changesCall struct {
	accountID                       uuid.UUID
	chainID, txType, status, userID string
	after                           TransactionPosition
	limit                           int
}

func newMemTransactions() *memTransactions {
	return &memTransactions{wallets: map[uuid.UUID]uuid.UUID{}}
}

func (m *memTransactions) ownWallet(walletID, accountID uuid.UUID) { m.wallets[walletID] = accountID }

func (m *memTransactions) failWith(err error) { m.err = err }

func (m *memTransactions) add(tx *models.Transaction) {
	m.rows = append(m.rows, tx)
}

func (m *memTransactions) FindByIdempotencyKey(_ context.Context, key string) (*models.Transaction, error) {
	if tx, ok := m.byKey[key]; ok {
		return tx, nil
	}
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

func (m *memTransactions) FindByIDForAccount(_ context.Context, id, accountID uuid.UUID) (*models.Transaction, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, tx := range m.rows {
		if tx.ID == id && m.wallets[tx.WalletID] == accountID {
			return tx, nil
		}
	}
	return nil, models.ErrRepositoryNotFound
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

// ListChangedForAccount reads the account's rows in (updated_at, id) order strictly
// after the position, like the repository; err, when set, fails it.
func (m *memTransactions) ListChangedForAccount(_ context.Context, accountID uuid.UUID, chainID, txType, status, userID string, afterUpdatedAt time.Time, afterID uuid.UUID, limit int) ([]models.Transaction, int64, error) {
	m.lastChangesCall = changesCall{
		accountID: accountID, chainID: chainID, txType: txType, status: status, userID: userID,
		after: TransactionPosition{UpdatedAt: afterUpdatedAt, ID: afterID}, limit: limit,
	}
	if m.err != nil {
		return nil, 0, m.err
	}
	matched := []models.Transaction{}
	for _, tx := range m.rows {
		if m.wallets[tx.WalletID] != accountID || tx.UpdatedAt == nil {
			continue
		}
		if (chainID != "" && tx.Chain != chainID) || (txType != "" && tx.TxType != txType) ||
			(status != "" && tx.Status != status) || (userID != "" && tx.ExternalUserID != userID) {
			continue
		}
		at := tx.UpdatedAt.StdTime()
		if at.Before(afterUpdatedAt) || (at.Equal(afterUpdatedAt) && bytes.Compare(tx.ID[:], afterID[:]) <= 0) {
			continue
		}
		matched = append(matched, *tx)
	}
	sort.Slice(matched, func(i, j int) bool {
		left, right := matched[i].UpdatedAt.StdTime(), matched[j].UpdatedAt.StdTime()
		if !left.Equal(right) {
			return left.Before(right)
		}
		return bytes.Compare(matched[i].ID[:], matched[j].ID[:]) < 0
	})
	total := int64(len(matched))
	if limit < len(matched) {
		matched = matched[:limit]
	}
	return matched, total, nil
}

type memWalletReader struct{}

func (memWalletReader) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, fmt.Errorf("wallet not found")
}
