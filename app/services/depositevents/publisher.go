// Package depositevents publishes the deposit lifecycle webhooks (deposit.pending,
// deposit.confirming, deposit.confirmed, deposit.failed) to the configs that may see
// the deposit's wallet, with the amount both as a decimal and in base units.
package depositevents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// AmountFormat tells receivers how to read Payload.Amount. Older payloads sent the raw
// transaction, whose amount was in base units and carried no decimals.
const AmountFormat = "decimal"

var (
	ErrUnsupportedEvent   = errors.New("not a deposit event")
	ErrNotADeposit        = errors.New("transaction is not a deposit")
	ErrInvalidBaseUnits   = errors.New("deposit amount is not a non-negative integer of base units")
	ErrUnknownDecimals    = errors.New("deposit asset decimals are unknown")
	ErrWalletScopeUnknown = errors.New("deposit wallet could not be loaded to scope the event")
)

var depositEvents = map[types.EventType]bool{
	types.EventDepositPending:    true,
	types.EventDepositConfirming: true,
	types.EventDepositConfirmed:  true,
	types.EventDepositFailed:     true,
}

type Enqueuer interface {
	EnqueueScoped(ctx context.Context, event webhook.ScopedEvent) (int, error)
}

type WalletStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
}

// AssetDecimals resolves how many decimals an asset uses on a chain.
type AssetDecimals interface {
	Decimals(chainID, asset string) (int, bool)
}

// Payload is the data object of every deposit webhook. Amount is the exact decimal
// rendering of AmountBaseUnits with Decimals; receivers should credit from
// AmountBaseUnits and Decimals and may use Amount as a cross-check.
type Payload struct {
	ID                    string  `json:"id"`
	TransactionID         string  `json:"transaction_id"`
	WalletID              string  `json:"wallet_id"`
	AddressID             *string `json:"address_id"`
	ExternalUserID        string  `json:"external_user_id"`
	Chain                 string  `json:"chain"`
	TxType                string  `json:"tx_type"`
	TxHash                string  `json:"tx_hash"`
	LogIndex              int     `json:"log_index"`
	FromAddress           string  `json:"from_address"`
	ToAddress             string  `json:"to_address"`
	Asset                 string  `json:"asset"`
	TokenContract         *string `json:"token_contract"`
	Amount                string  `json:"amount"`
	AmountFormat          string  `json:"amount_format"`
	AmountBaseUnits       string  `json:"amount_base_units"`
	Decimals              int     `json:"decimals"`
	BlockNumber           int64   `json:"block_number"`
	Confirmations         int     `json:"confirmations"`
	RequiredConfirmations int     `json:"required_confirmations"`
	Status                string  `json:"status"`
	ConfirmedAt           *string `json:"confirmed_at"`
	OccurredAt            string  `json:"occurred_at"`
}

type Publisher struct {
	enqueuer Enqueuer
	wallets  WalletStore
	decimals AssetDecimals
	now      func() time.Time
}

func NewPublisher(enqueuer Enqueuer, wallets WalletStore, decimals AssetDecimals) *Publisher {
	return &Publisher{
		enqueuer: enqueuer,
		wallets:  wallets,
		decimals: decimals,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Publish sends one deposit event to every subscribed config owned by the wallet's
// account (and to legacy configs without an owner), at most once per config per
// transaction. Nothing is sent when the amount cannot be stated unambiguously.
func (p *Publisher) Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error {
	if p.enqueuer == nil {
		return errors.New("publish deposit webhook: no enqueuer configured")
	}
	if !depositEvents[eventType] {
		return fmt.Errorf("publish %s: %w", eventType, ErrUnsupportedEvent)
	}
	if tx.ID == uuid.Nil || tx.WalletID == uuid.Nil {
		return fmt.Errorf("publish %s: transaction and wallet ids are required", eventType)
	}
	if tx.TxType != models.TxTypeDeposit {
		return fmt.Errorf("publish %s for transaction %s: %w", eventType, tx.ID, ErrNotADeposit)
	}

	payload, err := p.payload(tx)
	if err != nil {
		return fmt.Errorf("publish %s for transaction %s: %w", eventType, tx.ID, err)
	}
	accountID, err := p.walletAccount(ctx, tx.WalletID)
	if err != nil {
		return fmt.Errorf("publish %s for transaction %s: %w", eventType, tx.ID, err)
	}

	transactionID := tx.ID
	enqueued, err := p.enqueuer.EnqueueScoped(ctx, webhook.ScopedEvent{
		EventType:     eventType,
		SubjectID:     tx.ID.String(),
		WalletID:      tx.WalletID,
		AccountID:     accountID,
		TransactionID: &transactionID,
		Data:          payload,
	})
	if err != nil {
		return fmt.Errorf("publish %s: %w", eventType, err)
	}
	slog.Info("deposit webhook published", "event_type", eventType, "transaction_id", tx.ID, "deliveries", enqueued)
	return nil
}

func (p *Publisher) payload(tx models.Transaction) (Payload, error) {
	baseUnits, ok := amount.ParseBaseUnits(tx.Amount)
	if !ok {
		return Payload{}, ErrInvalidBaseUnits
	}
	if p.decimals == nil {
		return Payload{}, ErrUnknownDecimals
	}
	decimals, ok := p.decimals.Decimals(tx.Chain, tx.Asset)
	if !ok {
		return Payload{}, fmt.Errorf("%w: %s on %s", ErrUnknownDecimals, tx.Asset, tx.Chain)
	}

	payload := Payload{
		ID:                    tx.ID.String(),
		TransactionID:         tx.ID.String(),
		WalletID:              tx.WalletID.String(),
		ExternalUserID:        tx.ExternalUserID,
		Chain:                 tx.Chain,
		TxType:                tx.TxType,
		TxHash:                tx.TxHash,
		LogIndex:              tx.LogIndex,
		FromAddress:           tx.FromAddress,
		ToAddress:             tx.ToAddress,
		Asset:                 tx.Asset,
		Amount:                amount.FormatBaseUnits(baseUnits, decimals),
		AmountFormat:          AmountFormat,
		AmountBaseUnits:       baseUnits.String(),
		Decimals:              decimals,
		BlockNumber:           tx.BlockNumber,
		Confirmations:         tx.Confirmations,
		RequiredConfirmations: tx.RequiredConfs,
		Status:                tx.Status,
		OccurredAt:            p.now().Format(time.RFC3339),
	}
	if tx.AddressID != nil {
		addressID := tx.AddressID.String()
		payload.AddressID = &addressID
	}
	if contract := strings.TrimSpace(tx.TokenContract); contract != "" {
		payload.TokenContract = &contract
	}
	if tx.ConfirmedAt != nil {
		confirmedAt := tx.ConfirmedAt.UTC().Format(time.RFC3339)
		payload.ConfirmedAt = &confirmedAt
	}
	return payload, nil
}

// walletAccount returns the account that owns the wallet; a wallet without an account
// is only visible to legacy and wallet-scoped configs.
func (p *Publisher) walletAccount(ctx context.Context, walletID uuid.UUID) (*uuid.UUID, error) {
	if p.wallets == nil {
		return nil, ErrWalletScopeUnknown
	}
	wallet, err := p.wallets.FindByID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWalletScopeUnknown, err)
	}
	if wallet == nil {
		return nil, ErrWalletScopeUnknown
	}
	return wallet.AccountID, nil
}
