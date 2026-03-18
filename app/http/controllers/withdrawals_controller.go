package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/container"
	"github.com/macromarkets/vault/app/services/withdraw"
)

// CreateWithdrawal creates a new withdrawal request
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
