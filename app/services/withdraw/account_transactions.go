package withdraw

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AssetDecimals resolves how many decimals an asset uses on a chain.
type AssetDecimals interface {
	Decimals(chainID, asset string) (int, bool)
}

// AccountTransaction is a transaction of the caller's account and the decimals of
// its asset, which the deposit webhooks also carry. Decimals is nil when the asset
// is unknown.
type AccountTransaction struct {
	Transaction models.Transaction
	Decimals    *int
}

// TransactionPosition is a place in the (updated_at, id) order of transactions.
// The zero position comes before every transaction.
type TransactionPosition struct {
	UpdatedAt time.Time
	ID        uuid.UUID
}

// follows reports whether p comes after other in (updated_at, id) order. Ids
// compare byte by byte, as PostgreSQL compares uuids.
func (p TransactionPosition) follows(other TransactionPosition) bool {
	if !p.UpdatedAt.Equal(other.UpdatedAt) {
		return p.UpdatedAt.After(other.UpdatedAt)
	}
	return bytes.Compare(p.ID[:], other.ID[:]) > 0
}

// TransactionChangesInput selects one page of the account's transactions in the
// order they last changed. The page starts strictly after After, or at
// UpdatedSince (inclusive) when that comes later; both zero read from the start.
// The filters are optional; Limit is required.
type TransactionChangesInput struct {
	AccountID      uuid.UUID
	Chain          string
	TxType         string
	Status         string
	ExternalUserID string
	UpdatedSince   time.Time
	After          TransactionPosition
	Limit          int
}

// TransactionChangesPage is one page of the changes feed. Total counts the
// transactions from the page's start on, this page included. Next is the
// position of the page's last transaction when more follow, nil otherwise.
type TransactionChangesPage struct {
	Transactions []AccountTransaction
	Total        int64
	Next         *TransactionPosition
}

// ListTransactionChanges returns the account's transactions in (updated_at, id)
// order from the position the input names. Every write to a transaction moves its
// updated_at, so a transaction comes back after each change of status or
// confirmations. It fails without a limit or when the store fails.
func (s *Service) ListTransactionChanges(ctx context.Context, in TransactionChangesInput) (TransactionChangesPage, error) {
	if in.Limit <= 0 {
		return TransactionChangesPage{}, fmt.Errorf("list transaction changes: limit is required")
	}
	start := TransactionPosition{UpdatedAt: in.UpdatedSince}
	if in.After.follows(start) {
		start = in.After
	}

	rows, total, err := s.transactionRepo.ListChangedForAccount(ctx, in.AccountID, in.Chain, in.TxType, in.Status,
		in.ExternalUserID, start.UpdatedAt, start.ID, in.Limit+1)
	if err != nil {
		return TransactionChangesPage{}, fmt.Errorf("list transaction changes: %w", err)
	}
	if rows == nil {
		rows = []models.Transaction{}
	}

	page := TransactionChangesPage{Total: total}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		last := rows[len(rows)-1]
		if last.UpdatedAt == nil {
			return TransactionChangesPage{}, fmt.Errorf("list transaction changes: transaction %s has no updated_at", last.ID)
		}
		page.Next = &TransactionPosition{UpdatedAt: last.UpdatedAt.StdTime().UTC(), ID: last.ID}
	}
	page.Transactions = s.accountTransactions(rows)
	return page, nil
}

// accountTransactions pairs each row with its asset decimals, looking each
// (chain, asset) up once. A nil slice stays nil.
func (s *Service) accountTransactions(rows []models.Transaction) []AccountTransaction {
	if rows == nil {
		return nil
	}
	known := map[[2]string]*int{}
	out := make([]AccountTransaction, len(rows))
	for i := range rows {
		key := [2]string{rows[i].Chain, rows[i].Asset}
		decimals, seen := known[key]
		if !seen {
			decimals = s.assetDecimals(rows[i].Chain, rows[i].Asset)
			known[key] = decimals
		}
		out[i] = AccountTransaction{Transaction: rows[i], Decimals: decimals}
	}
	return out
}

// assetDecimals is the decimals of asset on chainID, nil when unknown.
func (s *Service) assetDecimals(chainID, asset string) *int {
	if s.decimals == nil {
		return nil
	}
	decimals, ok := s.decimals.Decimals(chainID, asset)
	if !ok {
		return nil
	}
	return &decimals
}
