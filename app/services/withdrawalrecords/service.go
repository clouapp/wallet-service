package withdrawalrecords

import (
	"context"
	"errors"
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

// Sentinel errors the HTTP layer maps to a refusal.
var (
	// ErrNotFound is a withdrawal that is missing, or that the caller's wallet
	// or account does not own. The two are not told apart.
	ErrNotFound = errors.New("withdrawal not found")
	// ErrNotPending is a cancel of a withdrawal that is no longer pending.
	ErrNotPending = errors.New("only pending withdrawals can be cancelled")
)

// WalletReader loads the wallet a withdrawal spends from.
type WalletReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// TransactionReader loads the transaction a withdrawal broadcast.
type TransactionReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
}

// Outcome is a withdrawal and, once it was broadcast, its transaction.
type Outcome struct {
	Withdrawal  *models.Withdrawal
	Transaction *models.Transaction
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
	wallets  WalletReader
	txs      TransactionReader
}

// Deps is everything the withdrawal record service uses. A nil Store is
// reported when a method runs, as the missing-repository error. Activity may
// be nil; Cancel then reports that the activity log is required. Wallets may
// be nil; FindInAccount then hides every withdrawal. Transactions may be nil;
// LookupInWallet then fails for a withdrawal that has a transaction.
type Deps struct {
	Store        Store
	Activity     ActivityLog
	Wallets      WalletReader
	Transactions TransactionReader
}

// NewRecords builds the withdrawal record service.
func NewRecords(deps Deps) *Records {
	return &Records{
		store:    deps.Store,
		activity: deps.Activity,
		wallets:  deps.Wallets,
		txs:      deps.Transactions,
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

// FindInWallet loads one withdrawal of the wallet. A missing row and a row of
// another wallet are ErrNotFound; a failed read is returned as an error.
func (s *Records) FindInWallet(ctx context.Context, walletID, withdrawalID uuid.UUID) (*models.Withdrawal, error) {
	if err := s.ready(ctx, "find withdrawal"); err != nil {
		return nil, err
	}
	withdrawal, err := s.store.FindByIDAndWallet(ctx, withdrawalID, walletID)
	if err := missingOrFailed(err, "find withdrawal"); err != nil {
		return nil, err
	}
	if withdrawal == nil {
		return nil, ErrNotFound
	}
	return withdrawal, nil
}

// missingOrFailed maps a repository lookup error: a missing row is ErrNotFound,
// any other error is returned wrapped so the caller answers a server failure.
func missingOrFailed(err error, op string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, models.ErrRepositoryNotFound):
		return ErrNotFound
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

// LookupInWallet loads one withdrawal of the wallet with the transaction it
// broadcast. A transaction row that is missing leaves Transaction nil; a failed
// read of it is an error, not ErrNotFound.
func (s *Records) LookupInWallet(ctx context.Context, walletID, withdrawalID uuid.UUID) (*Outcome, error) {
	withdrawal, err := s.FindInWallet(ctx, walletID, withdrawalID)
	if err != nil {
		return nil, err
	}
	outcome := &Outcome{Withdrawal: withdrawal}
	if withdrawal.TransactionID == nil {
		return outcome, nil
	}
	if s.txs == nil {
		return nil, fmt.Errorf("lookup withdrawal transaction: transactions reader is required")
	}
	tx, err := s.txs.FindByID(ctx, *withdrawal.TransactionID)
	if err != nil {
		return nil, fmt.Errorf("lookup withdrawal transaction: %w", err)
	}
	outcome.Transaction = tx
	return outcome, nil
}

// FindInAccount loads one withdrawal whose wallet belongs to the account. A
// missing row and a wallet of another account are ErrNotFound; a failed read
// is returned as an error.
func (s *Records) FindInAccount(ctx context.Context, accountID, withdrawalID uuid.UUID) (*models.Withdrawal, error) {
	if err := s.ready(ctx, "find withdrawal"); err != nil {
		return nil, err
	}
	withdrawal, err := s.store.FindByID(ctx, withdrawalID)
	if err := missingOrFailed(err, "find withdrawal"); err != nil {
		return nil, err
	}
	if withdrawal == nil || s.wallets == nil {
		return nil, ErrNotFound
	}
	wallet, err := s.wallets.FindByID(ctx, withdrawal.WalletID)
	if err := missingOrFailed(err, "find withdrawal wallet"); err != nil {
		return nil, err
	}
	if wallet == nil || wallet.AccountID == nil || *wallet.AccountID != accountID {
		return nil, ErrNotFound
	}
	return withdrawal, nil
}

// CancelPending cancels a pending withdrawal of the wallet on behalf of the
// actor and returns it as cancelled. ErrNotPending is a withdrawal already
// settled; nothing is written then.
func (s *Records) CancelPending(ctx context.Context, wallet *models.Wallet, actorID, withdrawalID uuid.UUID) (*models.Withdrawal, error) {
	if wallet == nil {
		return nil, fmt.Errorf("cancel withdrawal: wallet is required")
	}
	withdrawal, err := s.FindInWallet(ctx, wallet.ID, withdrawalID)
	if err != nil {
		return nil, err
	}
	if withdrawal.Status != "pending" {
		return nil, ErrNotPending
	}
	if wallet.AccountID == nil || actorID == uuid.Nil {
		return nil, fmt.Errorf("cancel withdrawal: wallet account and actor are required")
	}
	if err := s.Cancel(ctx, *wallet.AccountID, actorID, withdrawalID); err != nil {
		return nil, err
	}
	withdrawal.Status = "cancelled"
	return withdrawal, nil
}
