package controllers

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macromarkets/vault/app/container"
	"github.com/macromarkets/vault/app/http/requests"
	"github.com/macromarkets/vault/app/services/withdraw"
)

// CreateWithdrawal godoc
// @Summary      Create a withdrawal
// @Description  Signs and broadcasts a withdrawal synchronously using MPC co-signing. Passphrase is required to decrypt the customer's key share.
// @Tags         Withdrawals
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id    path      string                  true  "Wallet UUID"  format(uuid)
// @Param        body  body      CreateWithdrawalRequest true  "Withdrawal request"
// @Success      201   {object}  models.Transaction
// @Failure      400   {object}  ErrorResponse  "Missing fields or invalid amount"
// @Failure      401   {object}  ErrorResponse  "Invalid passphrase"
// @Failure      409   {object}  ErrorResponse  "Concurrent withdrawal in progress"
// @Failure      422   {object}  ErrorResponse  "Insufficient funds"
// @Failure      429   {object}  ErrorResponse  "Too many failed passphrase attempts"
// @Router       /v1/wallets/{id}/withdrawals [post]
func CreateWithdrawal(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("id"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req requests.CreateWithdrawalRequest
	validationErrors, err := ctx.Request().ValidateRequest(&req)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}
	if validationErrors != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, validationErrors.All())
	}

	tx, err := container.Get().WithdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
		WalletID:       walletID,
		ExternalUserID: req.ExternalUserID,
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		Passphrase:     req.Passphrase,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, withdraw.ErrInvalidPassphrase):
			return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrPassphraseTooShort):
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrInsufficientFunds):
			return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrConcurrentWithdraw):
			return ctx.Response().Json(http.StatusConflict, http.Json{"error": err.Error()})
		case errors.Is(err, withdraw.ErrTooManyAttempts):
			return ctx.Response().Json(http.StatusTooManyRequests, http.Json{"error": err.Error()})
		default:
			return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
		}
	}
	return ctx.Response().Json(http.StatusCreated, tx)
}

// CreateWithdrawalRequest is the request body for creating a withdrawal.
type CreateWithdrawalRequest struct {
	ExternalUserID string `json:"external_user_id" example:"user_123"`
	ToAddress      string `json:"to_address"       example:"0xABCDEF1234567890"`
	Amount         string `json:"amount"           example:"0.5"`
	Asset          string `json:"asset"            example:"eth"`
	Passphrase     string `json:"passphrase"       example:"strong-passphrase-min-12"`
	IdempotencyKey string `json:"idempotency_key"  example:"wdl_20260317_001"`
}
