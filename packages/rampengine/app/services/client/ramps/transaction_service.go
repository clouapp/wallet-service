package ramps

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// TransactionService lists and reads ramp orders across both directions.
type TransactionService struct {
	engine
}

// NewTransactionService wires the unified transaction read path.
func NewTransactionService(d Deps) *TransactionService {
	return &TransactionService{engine: newEngine(d)}
}

// List returns the customer's ramp orders, newest first, from the database
// only.
func (s *TransactionService) List(
	ctx context.Context,
	customerID uint,
	txType, status string,
	from, to *time.Time,
	page, perPage int,
) ([]OrderView, int64, error) {
	return s.page(repositories.TransactionListFilters{
		CustomerID: customerID, Type: txType, Status: status, RampOnly: txType == "",
		From: from, To: to, Page: page, PerPage: perPage,
	})
}

// Latest returns the customer's most recent ramp order, refreshed when still
// in flight.
func (s *TransactionService) Latest(ctx context.Context, customerID uint) (*OrderView, error) {
	tx, err := s.txRepo.FindLatestByCustomer(customerID)
	if err != nil {
		return nil, fmt.Errorf("ramps: load latest transaction: %w", err)
	}
	if tx == nil {
		return nil, ErrOrderNotFound
	}
	s.refreshInFlight(ctx, tx)
	return s.toOrderView(tx), nil
}
