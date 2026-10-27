// Package withdrawalevents publishes the public withdrawal lifecycle webhooks
// (withdrawal.broadcast, withdrawal.confirmed, withdrawal.failed) and keeps the
// withdrawals row in step with the on-chain outcome.
package withdrawalevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

type Enqueuer interface {
	EnqueueScoped(ctx context.Context, event webhook.ScopedEvent) (int, error)
}

// Stager is an Enqueuer that records the deliveries inside the caller's database
// transaction and returns the send to run after it commits (webhook.Service).
type Stager interface {
	StageScoped(ctx context.Context, event webhook.ScopedEvent) (func(context.Context), int, error)
}

type WithdrawalStore interface {
	FindByTransactionID(ctx context.Context, transactionID uuid.UUID) (*models.Withdrawal, error)
	FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	FindBroadcastWithConfirmedTransaction(ctx context.Context, limit int) ([]models.Withdrawal, error)
	MarkConfirmed(ctx context.Context, id uuid.UUID, transactionID uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error
}

// TransactionFailureGuard atomically refuses a stale execution's callback.
type TransactionFailureGuard interface {
	MarkFailedForTransaction(context.Context, uuid.UUID, uuid.UUID, string) error
}

// CommittedOutcomeStore commits an aggregate outcome and staged outbox before
// returning. It must refuse ambient outer transactions.
type CommittedOutcomeStore interface {
	WithinCommitted(context.Context, func(context.Context) error) error
}

type recoveryPredecessorLookup interface {
	FindByOriginalRecoveryTransactionID(context.Context, uuid.UUID) (*models.Withdrawal, error)
}

var errStaleWithdrawalTransaction = errors.New("withdrawal event belongs to a superseded transaction")

type TransactionStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
}

type WalletStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// AssetDecimals resolves how many decimals an asset uses on a chain.
type AssetDecimals interface {
	Decimals(chainID, asset string) (int, bool)
}

// Payload is the data object of every withdrawal lifecycle webhook.
// Amount is the human decimal string; AmountBaseUnits is the integer amount in the
// asset's smallest unit (wei, satoshi, lamport, token base units).
type Payload struct {
	WithdrawalID          string  `json:"withdrawal_id"`
	IdempotencyKey        string  `json:"idempotency_key"`
	WalletID              string  `json:"wallet_id"`
	TransactionID         *string `json:"transaction_id"`
	Chain                 string  `json:"chain"`
	Asset                 string  `json:"asset"`
	TokenContract         *string `json:"token_contract"`
	Amount                string  `json:"amount"`
	AmountBaseUnits       *string `json:"amount_base_units"`
	Decimals              *int    `json:"decimals"`
	DestinationAddress    string  `json:"destination_address"`
	DestinationTag        *uint32 `json:"destination_tag,omitempty"`
	TxHash                *string `json:"tx_hash"`
	Confirmations         int     `json:"confirmations"`
	RequiredConfirmations int     `json:"required_confirmations"`
	Status                string  `json:"status"`
	FailureCode           *string `json:"failure_code"`
	OccurredAt            string  `json:"occurred_at"`
	// BroadcastOutcome is "unknown" when the broadcast answer did not say whether the
	// network took the transfer: it is settled by its hash (show it as pending
	// verification). Left out otherwise.
	BroadcastOutcome *string `json:"broadcast_outcome,omitempty"`
}

// FailedAttempt describes what was requested for a withdrawal that never produced a transaction.
type FailedAttempt struct {
	Chain     string
	Asset     string
	BaseUnits *big.Int
}

type Publisher struct {
	enqueuer     Enqueuer
	withdrawals  WithdrawalStore
	transactions TransactionStore
	wallets      WalletStore
	decimals     AssetDecimals
	now          func() time.Time
}

// PublisherDeps is everything the withdrawal event publisher needs. A nil field means that
// dependency is absent.
type PublisherDeps struct {
	Enqueuer     Enqueuer
	Withdrawals  WithdrawalStore
	Transactions TransactionStore
	Wallets      WalletStore
	Decimals     AssetDecimals
}

// NewPublisher wires the withdrawal event publisher from PublisherDeps.
func NewPublisher(deps PublisherDeps) *Publisher {
	return &Publisher{
		enqueuer:     deps.Enqueuer,
		withdrawals:  deps.Withdrawals,
		transactions: deps.Transactions,
		wallets:      deps.Wallets,
		decimals:     deps.Decimals,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// PublishBroadcast announces that the withdrawal's transaction is on chain.
func (p *Publisher) PublishBroadcast(ctx context.Context, withdrawal *models.Withdrawal, tx *models.Transaction) error {
	if withdrawal == nil || withdrawal.ID == uuid.Nil {
		return errors.New("publish withdrawal.broadcast: withdrawal is required")
	}
	if tx == nil || strings.TrimSpace(tx.TxHash) == "" {
		return errors.New("publish withdrawal.broadcast: transaction with a hash is required")
	}
	payload := p.transactionPayload(withdrawal, tx, models.WithdrawalStatusBroadcast)
	return p.publish(ctx, types.EventWithdrawalBroadcast, withdrawal.WalletID, withdrawal.AccountID, &tx.ID, payload)
}

// PublishFailed announces a withdrawal that ended without a transaction. Only the public
// failure code is sent; internal error details never leave the service.
func (p *Publisher) PublishFailed(ctx context.Context, withdrawal *models.Withdrawal, failureCode string, attempt FailedAttempt) error {
	if withdrawal == nil || withdrawal.ID == uuid.Nil {
		return errors.New("publish withdrawal.failed: withdrawal is required")
	}
	code := strings.TrimSpace(failureCode)
	if code == "" {
		return errors.New("publish withdrawal.failed: failure code is required")
	}

	payload := Payload{
		WithdrawalID:       withdrawal.ID.String(),
		IdempotencyKey:     withdrawal.ID.String(),
		WalletID:           withdrawal.WalletID.String(),
		Chain:              attempt.Chain,
		Asset:              attempt.Asset,
		DestinationAddress: withdrawal.DestinationAddress,
		DestinationTag:     withdrawal.DestinationTag,
		Status:             models.WithdrawalStatusFailed,
		FailureCode:        &code,
		OccurredAt:         p.now().Format(time.RFC3339),
	}
	p.fillAmount(&payload, attempt.Chain, attempt.Asset, attempt.BaseUnits, withdrawal.Amount)
	return p.publish(ctx, types.EventWithdrawalFailed, withdrawal.WalletID, withdrawal.AccountID, nil, payload)
}

// MarkConfirmed publishes a confirmed current execution. Physical stores stage
// the event with the guarded aggregate outcome in one owned commit. Legacy stores
// retain publish-before-mark behavior; the per-subject dedup repairs backfill.
func (p *Publisher) MarkConfirmed(ctx context.Context, tx *models.Transaction) error {
	if tx == nil || tx.ID == uuid.Nil {
		return errors.New("mark withdrawal confirmed: transaction is required")
	}
	if tx.TxType != models.TxTypeWithdrawal {
		return fmt.Errorf("mark withdrawal confirmed: transaction %s is a %s", tx.ID, tx.TxType)
	}
	if tx.Status != string(types.TxStatusConfirmed) {
		return fmt.Errorf("mark withdrawal confirmed: transaction %s is %s", tx.ID, tx.Status)
	}

	withdrawal, err := p.withdrawalForTransaction(ctx, tx)
	if errors.Is(err, errStaleWithdrawalTransaction) {
		return nil
	}
	if err != nil {
		return err
	}
	if withdrawal != nil {
		if runner, ok := p.withdrawals.(CommittedOutcomeStore); ok {
			if stager, ok := p.enqueuer.(Stager); ok {
				return p.confirmCommitted(ctx, withdrawal, tx, runner, stager)
			}
		}
	}

	var accountID *uuid.UUID
	if withdrawal != nil {
		accountID = withdrawal.AccountID
	}
	payload := p.transactionPayload(withdrawal, tx, models.WithdrawalStatusConfirmed)
	if err := p.publish(ctx, types.EventWithdrawalConfirmed, tx.WalletID, accountID, &tx.ID, payload); err != nil {
		return err
	}

	if withdrawal == nil || withdrawal.Status == models.WithdrawalStatusConfirmed {
		return nil
	}
	if err := p.withdrawals.MarkConfirmed(ctx, withdrawal.ID, tx.ID); err != nil {
		return fmt.Errorf("mark withdrawal %s confirmed: %w", withdrawal.ID, err)
	}
	return nil
}

// StageFailed is called, with ctx bound to the database transaction that fails a
// withdrawal transaction on chain (a failed EVM receipt, an XRP tec result, a
// payment past its LastLedgerSequence, a transfer that can never land): it records
// withdrawal.failed with failureCode and the transaction and marks the withdrawal
// row failed, both in that transaction, and returns the send that queues the
// event, to run only once it commits (nil: nothing to send). A failure that loses a
// race is rolled back with them, so it leaves no withdrawal.failed. A withdrawal
// already failed is left alone and nothing is staged again; the per-subject dedup
// keeps one withdrawal.failed per config. The enqueuer must stage (Stager).
func (p *Publisher) StageFailed(ctx context.Context, tx *models.Transaction, failureCode string) (func(context.Context), error) {
	if tx == nil || tx.ID == uuid.Nil {
		return nil, errors.New("stage withdrawal failed: transaction is required")
	}
	if tx.TxType != models.TxTypeWithdrawal {
		return nil, fmt.Errorf("stage withdrawal failed: transaction %s is a %s", tx.ID, tx.TxType)
	}
	code := strings.TrimSpace(failureCode)
	if code == "" {
		return nil, errors.New("stage withdrawal failed: failure code is required")
	}
	stager, ok := p.enqueuer.(Stager)
	if !ok {
		return nil, errors.New("stage withdrawal failed: the enqueuer cannot stage")
	}

	withdrawal, err := p.withdrawalForTransaction(ctx, tx)
	if errors.Is(err, errStaleWithdrawalTransaction) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if withdrawal != nil && withdrawal.Status == models.WithdrawalStatusFailed {
		return nil, nil
	}

	var accountID *uuid.UUID
	if withdrawal != nil {
		accountID = withdrawal.AccountID
	}
	payload := p.transactionPayload(withdrawal, tx, models.WithdrawalStatusFailed)
	payload.FailureCode = &code
	send, staged, err := stager.StageScoped(ctx, p.scopedEvent(ctx, types.EventWithdrawalFailed, tx.WalletID, accountID, &tx.ID, payload))
	if err != nil {
		return nil, fmt.Errorf("stage %s: %w", types.EventWithdrawalFailed, err)
	}
	slog.Info("withdrawal webhook staged", "event_type", types.EventWithdrawalFailed, "withdrawal_id", payload.WithdrawalID, "deliveries", staged)
	if withdrawal == nil {
		return send, nil
	}
	if guard, ok := p.withdrawals.(TransactionFailureGuard); ok {
		if err := guard.MarkFailedForTransaction(ctx, withdrawal.ID, tx.ID, code); err != nil {
			return nil, fmt.Errorf("mark withdrawal %s failed: %w", withdrawal.ID, err)
		}
	} else if err := p.withdrawals.MarkFailed(ctx, withdrawal.ID, code); err != nil {
		return nil, fmt.Errorf("mark withdrawal %s failed: %w", withdrawal.ID, err)
	}
	return send, nil
}

// Backfill confirms withdrawals whose transaction the tracker confirmed but whose row
// is still broadcast (events lost to a crash, or confirmed before this publisher existed).
func (p *Publisher) Backfill(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("backfill withdrawal confirmations: limit must be positive")
	}
	withdrawals, err := p.withdrawals.FindBroadcastWithConfirmedTransaction(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("find withdrawals to backfill: %w", err)
	}

	confirmed := 0
	for _, withdrawal := range withdrawals {
		if ctx.Err() != nil {
			return confirmed, ctx.Err()
		}
		if withdrawal.TransactionID == nil {
			continue
		}
		tx, err := p.transactions.FindByID(ctx, *withdrawal.TransactionID)
		if err != nil || tx == nil {
			slog.Error("backfill withdrawal confirmation: load transaction", "withdrawal_id", withdrawal.ID, "error", err)
			continue
		}
		if err := p.MarkConfirmed(ctx, tx); err != nil {
			slog.Error("backfill withdrawal confirmation", "withdrawal_id", withdrawal.ID, "error", err)
			continue
		}
		confirmed++
	}
	return confirmed, nil
}

func (p *Publisher) withdrawalForTransaction(ctx context.Context, tx *models.Transaction) (*models.Withdrawal, error) {
	withdrawal, err := p.withdrawals.FindByTransactionID(ctx, tx.ID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("find withdrawal for transaction %s: %w", tx.ID, err)
	}
	if withdrawal != nil {
		return currentWithdrawalFor(tx, withdrawal)
	}
	if tx.IdempotencyKey != nil {
		withdrawalID, parseErr := uuid.Parse(strings.TrimSpace(*tx.IdempotencyKey))
		if parseErr == nil {
			withdrawal, err = p.withdrawals.FindByIDAndWallet(ctx, withdrawalID, tx.WalletID)
			if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
				return nil, fmt.Errorf("find withdrawal %s by idempotency key: %w", withdrawalID, err)
			}
			if withdrawal != nil {
				return currentWithdrawalFor(tx, withdrawal)
			}
		}
	}
	if predecessors, ok := p.withdrawals.(recoveryPredecessorLookup); ok {
		withdrawal, err = predecessors.FindByOriginalRecoveryTransactionID(ctx, tx.ID)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, fmt.Errorf("find withdrawal for recovery predecessor %s: %w", tx.ID, err)
		}
		if withdrawal != nil {
			return currentWithdrawalFor(tx, withdrawal)
		}
	}
	return nil, nil
}

func currentWithdrawalFor(tx *models.Transaction, w *models.Withdrawal) (*models.Withdrawal, error) {
	if w.WalletID != tx.WalletID || (w.TransactionID != nil && *w.TransactionID != tx.ID) {
		return nil, errStaleWithdrawalTransaction
	}
	return w, nil
}

// confirmCommitted avoids publishing a predecessor confirmation if the current
// link changes between lookup and CAS. An outbox failure rolls the outcome back;
// its queue send can only run after the owned transaction commits.
func (p *Publisher) confirmCommitted(ctx context.Context, w *models.Withdrawal, tx *models.Transaction, runner CommittedOutcomeStore, stager Stager) error {
	var send func(context.Context)
	err := runner.WithinCommitted(ctx, func(txCtx context.Context) error {
		if err := p.withdrawals.MarkConfirmed(txCtx, w.ID, tx.ID); err != nil {
			return err
		}
		payload := p.transactionPayload(w, tx, models.WithdrawalStatusConfirmed)
		var err error
		send, _, err = stager.StageScoped(txCtx, p.scopedEvent(txCtx, types.EventWithdrawalConfirmed, tx.WalletID, w.AccountID, &tx.ID, payload))
		return err
	})
	if errors.Is(err, models.ErrTransactionSettled) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("commit withdrawal %s confirmed: %w", w.ID, err)
	}
	if send != nil {
		send(ctx)
	}
	return nil
}

func (p *Publisher) transactionPayload(withdrawal *models.Withdrawal, tx *models.Transaction, status string) Payload {
	transactionID := tx.ID.String()
	txHash := tx.TxHash
	payload := Payload{
		WithdrawalID:          subjectID(withdrawal, tx),
		IdempotencyKey:        idempotencyKey(withdrawal, tx),
		WalletID:              tx.WalletID.String(),
		TransactionID:         &transactionID,
		Chain:                 tx.Chain,
		Asset:                 tx.Asset,
		DestinationAddress:    tx.ToAddress,
		DestinationTag:        tx.DestinationTag,
		TxHash:                &txHash,
		Confirmations:         tx.Confirmations,
		RequiredConfirmations: tx.RequiredConfs,
		Status:                status,
		OccurredAt:            p.now().Format(time.RFC3339),
	}
	if contract := strings.TrimSpace(tx.TokenContract); contract != "" {
		payload.TokenContract = &contract
	}
	if outcome := broadcastOutcomeOf(tx); outcome == chain.BroadcastOutcomeUnknown {
		payload.BroadcastOutcome = &outcome
	}

	requestedAmount := ""
	if withdrawal != nil {
		requestedAmount = withdrawal.Amount
	}
	baseUnits, ok := new(big.Int).SetString(strings.TrimSpace(tx.Amount), 10)
	if !ok {
		baseUnits = nil
	}
	p.fillAmount(&payload, tx.Chain, tx.Asset, baseUnits, requestedAmount)
	return payload
}

func (p *Publisher) fillAmount(payload *Payload, chainID, asset string, baseUnits *big.Int, requestedAmount string) {
	payload.Amount = amount.NormalizeDecimal(requestedAmount)
	if baseUnits == nil || baseUnits.Sign() < 0 {
		return
	}
	units := baseUnits.String()
	payload.AmountBaseUnits = &units

	if p.decimals == nil {
		return
	}
	decimals, ok := p.decimals.Decimals(chainID, asset)
	if !ok {
		slog.Warn("withdrawal webhook: unknown asset decimals, amount falls back to the requested value", "chain", chainID, "asset", asset)
		return
	}
	payload.Decimals = &decimals
	payload.Amount = amount.FormatBaseUnits(baseUnits, decimals)
}

func (p *Publisher) publish(ctx context.Context, eventType types.EventType, walletID uuid.UUID, accountID *uuid.UUID, transactionID *uuid.UUID, payload Payload) error {
	if p.enqueuer == nil {
		return errors.New("publish withdrawal webhook: no enqueuer configured")
	}
	enqueued, err := p.enqueuer.EnqueueScoped(ctx, p.scopedEvent(ctx, eventType, walletID, accountID, transactionID, payload))
	if err != nil {
		return fmt.Errorf("publish %s: %w", eventType, err)
	}
	slog.Info("withdrawal webhook published", "event_type", eventType, "withdrawal_id", payload.WithdrawalID, "deliveries", enqueued)
	return nil
}

// scopedEvent is the outbox event of a withdrawal webhook, scoped to the wallet's
// owning account.
func (p *Publisher) scopedEvent(ctx context.Context, eventType types.EventType, walletID uuid.UUID, accountID *uuid.UUID, transactionID *uuid.UUID, payload Payload) webhook.ScopedEvent {
	return webhook.ScopedEvent{
		EventType:     eventType,
		SubjectID:     payload.WithdrawalID,
		WalletID:      walletID,
		AccountID:     p.walletAccount(ctx, walletID, accountID),
		TransactionID: transactionID,
		Data:          payload,
	}
}

// walletAccount scopes the event to the wallet's owning account; the withdrawal's
// recorded account is only a fallback when the wallet cannot be read.
func (p *Publisher) walletAccount(ctx context.Context, walletID uuid.UUID, fallback *uuid.UUID) *uuid.UUID {
	if p.wallets == nil {
		return fallback
	}
	wallet, err := p.wallets.FindByID(ctx, walletID)
	if err != nil || wallet == nil {
		slog.Warn("withdrawal webhook: wallet lookup failed, using the withdrawal account", "wallet_id", walletID, "error", err)
		return fallback
	}
	if wallet.AccountID == nil {
		return fallback
	}
	return wallet.AccountID
}

func subjectID(withdrawal *models.Withdrawal, tx *models.Transaction) string {
	if withdrawal != nil {
		return withdrawal.ID.String()
	}
	return idempotencyKey(nil, tx)
}

func idempotencyKey(withdrawal *models.Withdrawal, tx *models.Transaction) string {
	if withdrawal != nil {
		return withdrawal.ID.String()
	}
	if tx.IdempotencyKey != nil && strings.TrimSpace(*tx.IdempotencyKey) != "" {
		return strings.TrimSpace(*tx.IdempotencyKey)
	}
	return tx.ID.String()
}

// broadcastOutcomeOf is the broadcast outcome the transaction's raw_payload keeps
// ("unknown"), or "".
func broadcastOutcomeOf(tx *models.Transaction) string {
	var payload map[string]json.RawMessage
	var outcome string
	if json.Unmarshal([]byte(tx.RawPayload), &payload) == nil {
		_ = json.Unmarshal(payload[chain.BroadcastOutcomePayloadKey], &outcome)
	}
	return outcome
}
