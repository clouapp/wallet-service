// Package withdrawalevents publishes the public withdrawal lifecycle webhooks
// (withdrawal.broadcast, withdrawal.confirmed, withdrawal.failed) and keeps the
// withdrawals row in step with the on-chain outcome.
package withdrawalevents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

type Enqueuer interface {
	EnqueueScoped(ctx context.Context, event webhook.ScopedEvent) (int, error)
}

type WithdrawalStore interface {
	FindByTransactionID(ctx context.Context, transactionID uuid.UUID) (*models.Withdrawal, error)
	FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	FindBroadcastWithConfirmedTransaction(ctx context.Context, limit int) ([]models.Withdrawal, error)
	MarkConfirmed(ctx context.Context, id uuid.UUID, transactionID uuid.UUID) error
}

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
	TxHash                *string `json:"tx_hash"`
	Confirmations         int     `json:"confirmations"`
	RequiredConfirmations int     `json:"required_confirmations"`
	Status                string  `json:"status"`
	FailureCode           *string `json:"failure_code"`
	OccurredAt            string  `json:"occurred_at"`
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
		Status:             models.WithdrawalStatusFailed,
		FailureCode:        &code,
		OccurredAt:         p.now().Format(time.RFC3339),
	}
	p.fillAmount(&payload, attempt.Chain, attempt.Asset, attempt.BaseUnits, withdrawal.Amount)
	return p.publish(ctx, types.EventWithdrawalFailed, withdrawal.WalletID, withdrawal.AccountID, nil, payload)
}

// MarkConfirmed is called once a withdrawal transaction reached its required
// confirmations: it publishes withdrawal.confirmed and then marks the withdrawal row
// confirmed. Publishing first means a crash in between is repaired by the backfill,
// and the per-subject dedup keeps that repair from delivering twice.
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
	if err != nil {
		return err
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
	if withdrawal != nil || tx.IdempotencyKey == nil {
		return withdrawal, nil
	}

	withdrawalID, parseErr := uuid.Parse(strings.TrimSpace(*tx.IdempotencyKey))
	if parseErr != nil {
		return nil, nil
	}
	withdrawal, err = p.withdrawals.FindByIDAndWallet(ctx, withdrawalID, tx.WalletID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, fmt.Errorf("find withdrawal %s by idempotency key: %w", withdrawalID, err)
	}
	return withdrawal, nil
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
		TxHash:                &txHash,
		Confirmations:         tx.Confirmations,
		RequiredConfirmations: tx.RequiredConfs,
		Status:                status,
		OccurredAt:            p.now().Format(time.RFC3339),
	}
	if contract := strings.TrimSpace(tx.TokenContract); contract != "" {
		payload.TokenContract = &contract
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
	scopeAccount := p.walletAccount(ctx, walletID, accountID)
	enqueued, err := p.enqueuer.EnqueueScoped(ctx, webhook.ScopedEvent{
		EventType:     eventType,
		SubjectID:     payload.WithdrawalID,
		WalletID:      walletID,
		AccountID:     scopeAccount,
		TransactionID: transactionID,
		Data:          payload,
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", eventType, err)
	}
	slog.Info("withdrawal webhook published", "event_type", eventType, "withdrawal_id", payload.WithdrawalID, "deliveries", enqueued)
	return nil
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
