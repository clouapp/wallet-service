package withdrawalrecords

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

// ActivityLog appends one row on the caller's transaction.
type ActivityLog interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	Append(ctx context.Context, row models.AccountActivity) error
}

// Store is the withdrawal row persistence. Signing and broadcast publication
// stay in the handler; these methods only read and update the row.
type Store interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	FindByWallet(ctx context.Context, walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error)
	FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	Create(ctx context.Context, withdrawal *models.Withdrawal) error
	RetryBroadcast(ctx context.Context, id uuid.UUID, amount, destination, feeEstimate, note string) error
	MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error
	MarkBroadcast(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
}

// Records reads and updates withdrawal rows.
type Records struct {
	store    Store
	activity ActivityLog
}

// Deps is everything the withdrawal record service uses. A nil Store is
// reported when a method runs, as the missing-repository error. Activity may
// be nil; Cancel then reports that the activity log is required.
type Deps struct {
	Store    Store
	Activity ActivityLog
}

// NewRecords builds the withdrawal record service.
func NewRecords(deps Deps) *Records {
	return &Records{
		store:    deps.Store,
		activity: deps.Activity,
	}
}

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

func (s *Records) FindByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	if err := s.ready(ctx, "find withdrawal"); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, id)
}

func (s *Records) FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	if err := s.ready(ctx, "find withdrawal"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndWallet(ctx, withdrawalID, walletID)
}

// Within runs fn inside one transaction on the withdrawal table. A new row
// and a broadcast retry each open their own call.
func (s *Records) Within(ctx context.Context, fn func(context.Context) error) error {
	if err := s.ready(ctx, "create withdrawal"); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("create withdrawal: transaction callback is required")
	}
	return s.store.Within(ctx, fn)
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

// Cancel marks a withdrawal cancelled and writes withdrawal.cancelled on the
// account trail in the same transaction. The amount is not stored.
func (s *Records) Cancel(ctx context.Context, accountID, actorID, withdrawalID uuid.UUID) error {
	if err := s.ready(ctx, "cancel withdrawal"); err != nil {
		return err
	}
	if s.activity == nil {
		return fmt.Errorf("cancel withdrawal: activity log is required")
	}
	if accountID == uuid.Nil || actorID == uuid.Nil || withdrawalID == uuid.Nil {
		return fmt.Errorf("cancel withdrawal: account, actor and withdrawal are required")
	}
	return s.activity.Within(ctx, func(ctx context.Context) error {
		if err := s.store.SetStatus(ctx, withdrawalID, "cancelled"); err != nil {
			return err
		}
		meta, err := activitylog.WithdrawalCancelled()
		if err != nil {
			return err
		}
		account := accountID
		return s.activity.Append(ctx, models.AccountActivity{
			AccountID:   &account,
			ActorUserID: actorID,
			Action:      activitylog.ActionWithdrawalCancelled,
			TargetType:  activitylog.TargetWithdrawal,
			TargetID:    withdrawalID.String(),
			Metadata:    meta,
		})
	})
}
