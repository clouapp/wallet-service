package controllers

import (
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	withdrawalresource "github.com/macrowallets/waas/app/http/resources/withdrawals"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
)

// Webhook publishing never changes the HTTP outcome: the withdrawal is already
// persisted, and the confirmation tracker backfill repairs a lost confirmation.
func publishWithdrawalBroadcast(ctx http.Context, publisher *withdrawalevents.Publisher, w *models.Withdrawal, tx *models.Transaction) {
	if publisher == nil || tx == nil {
		return
	}
	if err := publisher.PublishBroadcast(ctx.Context(), w, tx); err != nil {
		slog.Error("publish withdrawal.broadcast", "withdrawal_id", w.ID, "error", err)
	}
}

func publishWithdrawalFailed(ctx http.Context, publisher *withdrawalevents.Publisher, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	if publisher == nil {
		return
	}
	if err := publisher.PublishFailed(ctx.Context(), w, failureCode, attempt); err != nil {
		slog.Error("publish withdrawal.failed", "withdrawal_id", w.ID, "error", err)
	}
}

func withdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	return withdraw.WithdrawalIDFromIdempotencyKey(idempotencyKey)
}

// PublishWithdrawalBroadcast, PublishWithdrawalFailed and
// WithdrawalIDFromIdempotencyKey are shared by the dashboard and external
// withdrawal handlers so both surfaces keep the same bytes.
func PublishWithdrawalBroadcast(ctx http.Context, publisher *withdrawalevents.Publisher, w *models.Withdrawal, tx *models.Transaction) {
	publishWithdrawalBroadcast(ctx, publisher, w, tx)
}

func PublishWithdrawalFailed(ctx http.Context, publisher *withdrawalevents.Publisher, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	publishWithdrawalFailed(ctx, publisher, w, failureCode, attempt)
}

func WithdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	return withdraw.WithdrawalIDFromIdempotencyKey(idempotencyKey)
}

// ---- Request/Response types ----

type CreateWalletWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"bc1q..."`
	Note               string `json:"note,omitempty" example:"Monthly payment"`
	Passphrase         string `json:"passphrase" example:"my-secure-wallet-passphrase"`
	TotpCode           string `json:"totp_code" example:"123456"`
	Asset              string `json:"asset,omitempty" example:"USDT"`
}

type EstimateWithdrawalSwagger struct {
	Amount             string `json:"amount" example:"0.001"`
	DestinationAddress string `json:"destination_address" example:"tb1q..."`
}

type WithdrawalListResponse struct {
	Data []withdrawalresource.Withdrawal `json:"data"`
}
