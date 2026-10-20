package withdraw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/mpcshare"
)

// ErrUnauthenticated is a caller that is neither a dashboard user nor an
// account (access token): there is nobody to attribute the withdrawal to.
var ErrUnauthenticated = errors.New("unauthenticated")

// ExecuteError is a failure of Request after Create wrote the row. The row is
// already marked failed. Unwrap gives the sentinel Request returned.
type ExecuteError struct {
	Err error
}

func (e *ExecuteError) Error() string {
	if e == nil || e.Err == nil {
		return "execute withdrawal"
	}
	return e.Err.Error()
}

func (e *ExecuteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SubmitRows marks the row Create wrote once Request has an outcome.
type SubmitRows interface {
	MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error
	MarkBroadcast(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error
}

// SubmitEvents announces the outcome. Publishing never changes the result: the
// row is already stored, and the confirmation tracker backfill repairs a lost
// event.
type SubmitEvents interface {
	PublishBroadcast(ctx context.Context, withdrawal *models.Withdrawal, tx *models.Transaction) error
	PublishFailed(ctx context.Context, withdrawal *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) error
}

// SubmitInput is the validated withdrawal body plus the caller the handler
// already authenticated. CallerUserID is the dashboard session user, nil for
// an access-token caller. CallerAccountID is the authenticated account; a
// nil one falls back to the wallet's account. APIToken carries the per-token
// spending cap and is nil for a dashboard caller.
type SubmitInput struct {
	Wallet             *models.Wallet
	CallerUserID       uuid.UUID
	CallerAccountID    uuid.UUID
	APIToken           *models.AccessToken
	TotpCode           string
	Passphrase         string
	Asset              string
	Amount             string
	DestinationAddress string
	DestinationTag     *uint32
	Note               string
	IdempotencyKey     string
}

// SubmitResult is the withdrawal row after Submit. Replayed means the row was
// already broadcast or confirmed and nothing was signed.
type SubmitResult struct {
	Withdrawal *models.Withdrawal
	Replayed   bool
}

// UseSubmit attaches the row writer and the event publisher Submit needs.
// Events may be nil: nothing is published.
func (s *Service) UseSubmit(rows SubmitRows, events SubmitEvents) {
	if s == nil {
		return
	}
	s.submitRows = rows
	s.submitEvents = events
}

// Submit creates the withdrawal row (Create), signs and broadcasts it
// (Request), and records the outcome on the row. Every failure is typed: a
// Create refusal comes back as *CreateRefusal, an unknown chain as
// chainregistry.ErrUnknownChain, a failed row read or write, or any other
// Create failure, as *CreateRowError, and a Request failure as *ExecuteError
// after the row is marked failed and withdrawal.failed is published.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*SubmitResult, error) {
	defer mpcshare.DiscardPassphrase(&in.Passphrase)
	if s == nil {
		return nil, fmt.Errorf("submit withdrawal: service is required")
	}
	if in.Wallet == nil {
		return nil, fmt.Errorf("submit withdrawal: wallet is required")
	}
	if in.CallerUserID == uuid.Nil && in.CallerAccountID == uuid.Nil {
		return nil, ErrUnauthenticated
	}
	if s.submitRows == nil {
		return nil, fmt.Errorf("submit withdrawal: withdrawal rows are required")
	}
	callerAccountID := in.CallerAccountID
	if callerAccountID == uuid.Nil && in.Wallet.AccountID != nil {
		callerAccountID = *in.Wallet.AccountID
	}
	release, err := s.holdIdempotencyKey(ctx, in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	defer release()

	created, err := s.Create(ctx, CreateInput{
		Wallet:             in.Wallet,
		DashboardUserID:    in.CallerUserID,
		TotpCode:           in.TotpCode,
		Passphrase:         in.Passphrase,
		Asset:              in.Asset,
		Amount:             in.Amount,
		DestinationAddress: in.DestinationAddress,
		DestinationTag:     in.DestinationTag,
		Note:               in.Note,
		IdempotencyKey:     in.IdempotencyKey,
	})
	if err != nil {
		return nil, classifyCreateError(err)
	}
	if created == nil || created.Withdrawal == nil || created.Resolved == nil || created.Resolved.BaseUnits == nil {
		return nil, &CreateRowError{Endpoint: "create_wallet_withdrawal", Err: fmt.Errorf("create withdrawal: empty result")}
	}
	if created.Replayed {
		return &SubmitResult{Withdrawal: created.Withdrawal, Replayed: true}, nil
	}
	resolved := created.Resolved
	w := created.Withdrawal

	request := WithdrawRequest{
		WalletID:        in.Wallet.ID,
		ToAddress:       in.DestinationAddress,
		DestinationTag:  in.DestinationTag,
		Amount:          resolved.BaseUnits.String(),
		Asset:           resolved.WalletAsset,
		Passphrase:      in.Passphrase,
		IdempotencyKey:  created.IdempotencyKey,
		WithdrawalID:    w.ID,
		CallerAccountID: callerAccountID,
	}
	if in.APIToken != nil {
		request = request.WithAPIToken(in.APIToken, in.Amount)
	}
	tx, _, err := s.Request(ctx, request)
	mpcshare.DiscardPassphrase(&request.Passphrase)
	if errors.Is(err, sweep.ErrSendUnverified) {
		// The transfer of an earlier try may be on its way: the withdrawal is neither
		// failed (and refunded) nor signed again.
		slog.Warn("withdrawal retry waits for its earlier send to be verified", "withdrawal_id", w.ID, "error", err)
		return nil, &ExecuteError{Err: err}
	}
	if err != nil {
		failureCode := FailureCode(err)
		if markErr := s.submitRows.MarkFailed(ctx, w.ID, failureCode); markErr != nil {
			return nil, &CreateRowError{
				Endpoint: "mark_withdrawal_failed",
				Err:      fmt.Errorf("execute withdrawal: %v; mark failed: %w", err, markErr),
			}
		}
		w.Status = models.WithdrawalStatusFailed
		s.publishFailed(ctx, w, failureCode, withdrawalevents.FailedAttempt{
			Chain:     in.Wallet.Chain,
			Asset:     resolved.WalletAsset,
			BaseUnits: resolved.BaseUnits,
		})
		return nil, &ExecuteError{Err: err}
	}

	w.Status = models.WithdrawalStatusBroadcast
	if tx != nil {
		w.TransactionID = &tx.ID
		w.TxHash = tx.TxHash
	}
	if markErr := s.submitRows.MarkBroadcast(ctx, w.ID, w.TransactionID); markErr != nil {
		return nil, &CreateRowError{Endpoint: "persist_broadcast_withdrawal", Err: markErr}
	}
	s.publishBroadcast(ctx, w, tx)
	return &SubmitResult{Withdrawal: w}, nil
}

func (s *Service) publishBroadcast(ctx context.Context, w *models.Withdrawal, tx *models.Transaction) {
	if s.submitEvents == nil || tx == nil {
		return
	}
	if err := s.submitEvents.PublishBroadcast(ctx, w, tx); err != nil {
		slog.Error("publish withdrawal.broadcast", "withdrawal_id", w.ID, "error", err)
	}
}

func (s *Service) publishFailed(ctx context.Context, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	if s.submitEvents == nil {
		return
	}
	if err := s.submitEvents.PublishFailed(ctx, w, failureCode, attempt); err != nil {
		slog.Error("publish withdrawal.failed", "withdrawal_id", w.ID, "error", err)
	}
}

// classifyCreateError keeps the errors a caller can tell apart and wraps the
// rest as a row error, which is logged and answered as a generic failure.
func classifyCreateError(err error) error {
	var refusal *CreateRefusal
	var row *CreateRowError
	switch {
	case errors.Is(err, chainregistry.ErrUnknownChain), errors.As(err, &refusal), errors.As(err, &row):
		return err
	}
	return &CreateRowError{Endpoint: "create_wallet_withdrawal", Err: err}
}

const (
	// A submit waits at most sameKeyWait (under the API gateway's 29 s) for another
	// one with its idempotency key, polling every sameKeyPoll; the key's lock frees
	// itself after sameKeyLockTTL if its holder died.
	sameKeyWait    = 25 * time.Second
	sameKeyPoll    = 100 * time.Millisecond
	sameKeyLockTTL = 3 * time.Minute
)

// holdIdempotencyKey serializes the submits of one idempotency key: a second one
// waits for the first to finish (its transfer recorded, sent and marked) and then
// sees its outcome, instead of replaying a row whose bytes have not left yet. A
// submit without a key is never held. A key still held after sameKeyWait is
// answered as a withdrawal in progress (nothing is marked).
func (s *Service) holdIdempotencyKey(ctx context.Context, key string) (func(), error) {
	key = strings.TrimSpace(key)
	if key == "" || s.cache == nil {
		return func() {}, nil
	}
	lockKey := "vault:lock:withdrawal-key:" + key
	deadline := time.Now().Add(sameKeyWait)
	for {
		acquired, err := cacheguard.Acquire(s.cache, lockKey, sameKeyLockTTL)
		if err != nil {
			return nil, &CreateRowError{Endpoint: "withdrawal_key_lock", Err: err}
		}
		if acquired {
			return func() { s.cache.Forget(lockKey) }, nil
		}
		if time.Now().After(deadline) {
			return nil, &ExecuteError{Err: fmt.Errorf("%w: another submit with this idempotency key is still running", ErrConcurrentWithdraw)}
		}
		timer := time.NewTimer(sameKeyPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
