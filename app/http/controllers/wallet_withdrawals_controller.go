package controllers

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
)

// Webhook publishing never changes the HTTP outcome: the withdrawal is already
// persisted, and the confirmation tracker backfill repairs a lost confirmation.
func publishWithdrawalBroadcast(ctx http.Context, w *models.Withdrawal, tx *models.Transaction) {
	publisher := container.Get().WithdrawalEvents
	if publisher == nil || tx == nil {
		return
	}
	if err := publisher.PublishBroadcast(ctx.Context(), w, tx); err != nil {
		slog.Error("publish withdrawal.broadcast", "withdrawal_id", w.ID, "error", err)
	}
}

func publishWithdrawalFailed(ctx http.Context, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	publisher := container.Get().WithdrawalEvents
	if publisher == nil {
		return
	}
	if err := publisher.PublishFailed(ctx.Context(), w, failureCode, attempt); err != nil {
		slog.Error("publish withdrawal.failed", "withdrawal_id", w.ID, "error", err)
	}
}

func withdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return uuid.New(), nil
	}
	id, err := uuid.Parse(idempotencyKey)
	if err != nil {
		return uuid.Nil, fmt.Errorf("idempotency_key must be a UUID")
	}
	return id, nil
}

func verifyWalletPassphrase(ctx http.Context, wallet *models.Wallet, passphrase string) http.Response {
	rdb := container.Get().Redis
	key := fmt.Sprintf("vault:ratelimit:passphrase:%s", wallet.ID)

	if rdb != nil {
		count, err := rdb.Get(ctx.Context(), key).Int()
		if err == nil && count >= 5 {
			return responses.Send(ctx, http.StatusTooManyRequests, http.Json{"error": "too many failed attempts, try again later"})
		}
	}

	shareA, decErr := wallet.DecryptShareA(passphrase)
	if decErr != nil {
		if errors.Is(decErr, mpcpkg.ErrInvalidPassphrase) {
			if rdb != nil {
				pipe := rdb.Pipeline()
				pipe.Incr(ctx.Context(), key)
				pipe.Expire(ctx.Context(), key, 60*time.Second)
				_, _ = pipe.Exec(ctx.Context())
			}
			return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid passphrase"})
		}
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "internal error"})
	}
	for i := range shareA {
		shareA[i] = 0
	}

	return nil
}

// VerifyWalletPassphrase, PublishWithdrawalBroadcast, PublishWithdrawalFailed
// and WithdrawalIDFromIdempotencyKey are shared by the dashboard and external
// withdrawal handlers so both surfaces keep the same bytes.
func VerifyWalletPassphrase(ctx http.Context, wallet *models.Wallet, passphrase string) http.Response {
	return verifyWalletPassphrase(ctx, wallet, passphrase)
}

func PublishWithdrawalBroadcast(ctx http.Context, w *models.Withdrawal, tx *models.Transaction) {
	publishWithdrawalBroadcast(ctx, w, tx)
}

func PublishWithdrawalFailed(ctx http.Context, w *models.Withdrawal, failureCode string, attempt withdrawalevents.FailedAttempt) {
	publishWithdrawalFailed(ctx, w, failureCode, attempt)
}

func WithdrawalIDFromIdempotencyKey(idempotencyKey string) (uuid.UUID, error) {
	return withdrawalIDFromIdempotencyKey(idempotencyKey)
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
	Data []models.Withdrawal `json:"data"`
}
