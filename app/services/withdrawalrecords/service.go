package withdrawalrecords

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Store is the withdrawal row persistence. Signing and broadcast publication
// stay in the handler; these methods only read and update the row.
type Store interface {
	FindByWallet(ctx context.Context, walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error)
	FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	Create(ctx context.Context, withdrawal *models.Withdrawal) error
	RetryBroadcast(ctx context.Context, id uuid.UUID, amount, destination, feeEstimate, note string) error
	MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error
	MarkBroadcast(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
}

// Records reads and updates withdrawal rows.
type Records struct{ store Store }

// NewRecords builds the withdrawal record service.
func NewRecords(store Store) *Records { return &Records{store: store} }

func (s *Records) ready(ctx context.Context, op string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("%s: withdrawals repository is required", op)
	}
	return nil
}

func (s *Records) FindByWallet(ctx context.Context, walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error) {
	if err := s.ready(ctx, "list withdrawals"); err != nil {
		return nil, 0, err
	}
	return s.store.FindByWallet(ctx, walletID, status, limit, offset)
}

func (s *Records) FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	if err := s.ready(ctx, "find withdrawal"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndWallet(ctx, withdrawalID, walletID)
}

func (s *Records) Create(ctx context.Context, withdrawal *models.Withdrawal) error {
	if err := s.ready(ctx, "create withdrawal"); err != nil {
		return err
	}
	return s.store.Create(ctx, withdrawal)
}

func (s *Records) RetryBroadcast(ctx context.Context, id uuid.UUID, amount, destination, feeEstimate, note string) error {
	if err := s.ready(ctx, "retry withdrawal broadcast"); err != nil {
		return err
	}
	return s.store.RetryBroadcast(ctx, id, amount, destination, feeEstimate, note)
}

func (s *Records) MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error {
	if err := s.ready(ctx, "mark withdrawal failed"); err != nil {
		return err
	}
	return s.store.MarkFailed(ctx, id, failureReason)
}

func (s *Records) MarkBroadcast(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error {
	if err := s.ready(ctx, "mark withdrawal broadcast"); err != nil {
		return err
	}
	return s.store.MarkBroadcast(ctx, id, transactionID)
}

func (s *Records) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if err := s.ready(ctx, "set withdrawal status"); err != nil {
		return err
	}
	return s.store.SetStatus(ctx, id, status)
}
