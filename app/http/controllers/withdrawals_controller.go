package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
	"github.com/macromarkets/vault/app/services/withdraw"
)

// CreateWithdrawal godoc
// @Summary      Create a withdrawal
// @Description  Enqueues a withdrawal request for the given wallet. The transaction is signed and broadcast asynchronously by the withdrawal worker.
// @Tags         Withdrawals
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id    path      string                  true  "Wallet UUID"  format(uuid)
// @Param        body  body      CreateWithdrawalRequest true  "Withdrawal request"
// @Success      201   {object}  models.Transaction
// @Failure      400   {object}  ErrorResponse  "Invalid wallet ID, missing fields, or insufficient funds"
// @Router       /v1/wallets/{id}/withdrawals [post]
func CreateWithdrawal(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("id"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req struct {
		ExternalUserID string `json:"external_user_id" form:"external_user_id"`
		ToAddress      string `json:"to_address" form:"to_address"`
		Amount         string `json:"amount" form:"amount"`
		Asset          string `json:"asset" form:"asset"`
		IdempotencyKey string `json:"idempotency_key" form:"idempotency_key"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": err.Error(),
		})
	}
	if req.ExternalUserID == "" || req.ToAddress == "" || req.Amount == "" || req.Asset == "" || req.IdempotencyKey == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "external_user_id, to_address, amount, asset, and idempotency_key are required",
		})
	}

	tx, err := container.Get().WithdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
		WalletID:       walletID,
		ExternalUserID: req.ExternalUserID,
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Json(http.StatusCreated, tx)
}

// CreateWithdrawalRequest is the request body for creating a withdrawal.
type CreateWithdrawalRequest struct {
	ExternalUserID string `json:"external_user_id" example:"user_123"`
	ToAddress      string `json:"to_address"       example:"0xABCDEF1234567890"`
	Amount         string `json:"amount"           example:"0.5"`
	Asset          string `json:"asset"            example:"eth"`
	IdempotencyKey string `json:"idempotency_key"  example:"wdl_20260317_001"`
}
