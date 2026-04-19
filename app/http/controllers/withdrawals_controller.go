package controllers

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// CreateWithdrawal godoc
// @Summary      Create a withdrawal
// @Description  Signs and broadcasts a withdrawal synchronously using MPC co-signing. Passphrase is required to decrypt the customer's key share. When the sweep planner must consolidate child addresses to fund the withdrawal, the response surfaces the `strategy` chosen and the `sweeps` legs executed before the final transaction.
// @Tags         Withdrawals
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        id    path      string                  true  "Wallet UUID"  format(uuid)
// @Param        body  body      CreateWithdrawalRequest true  "Withdrawal request"
// @Success      201   {object}  WithdrawalResponse
// @Failure      400   {object}  ErrorResponse  "Missing fields or invalid amount"
// @Failure      401   {object}  ErrorResponse  "Invalid passphrase"
// @Failure      409   {object}  ErrorResponse  "Concurrent withdrawal in progress"
// @Failure      422   {object}  ErrorResponse  "Insufficient funds or wallet not gas-ready"
// @Failure      429   {object}  ErrorResponse  "Too many failed passphrase attempts"
// @Router       /v1/wallets/{id}/withdrawals [post]
func CreateWithdrawal(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "invalid wallet id",
		})
	}

	var req requests.CreateWithdrawalRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	tx, meta, err := container.Get().WithdrawalService.Request(ctx.Context(), withdraw.WithdrawRequest{
		WalletID:       walletID,
		ExternalUserID: req.ExternalUserID,
		ToAddress:      req.ToAddress,
		Amount:         req.Amount,
		Asset:          req.Asset,
		Passphrase:     req.Passphrase,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		if resp := MapSweepError(ctx, err); resp != nil {
			return resp
		}
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
			return MapInternalError(ctx, err, "create_withdrawal")
		}
	}

	return ctx.Response().Json(http.StatusCreated, buildWithdrawalResponse(tx, meta))
}

// buildWithdrawalResponse composes the wire shape of a withdrawal creation
// response. It flattens the final transaction plus sweep metadata so callers
// receive a single object containing both the on-chain broadcast details and
// the source-selection outcome (strategy + completed legs).
func buildWithdrawalResponse(tx *models.Transaction, meta *withdraw.Metadata) http.Json {
	resp := http.Json{
		"transaction_id": tx.ID.String(),
		"tx_hash":        tx.TxHash,
		"status":         tx.Status,
		"origin":         tx.Origin,
	}
	if meta == nil {
		return resp
	}
	if meta.Strategy != "" {
		resp["strategy"] = string(meta.Strategy)
	}
	if len(meta.Sweeps) > 0 {
		sweeps := make([]http.Json, 0, len(meta.Sweeps))
		for _, sw := range meta.Sweeps {
			sweeps = append(sweeps, http.Json{
				"tx_id":   sw.InternalTxID.String(),
				"tx_hash": sw.TxHash,
				"from":    sw.From.Address,
				"origin":  "sweep",
			})
		}
		resp["sweeps"] = sweeps
	}
	if meta.FailedStep != nil {
		resp["failed_step"] = http.Json{
			"index":       meta.FailedStep.Index,
			"last_error":  meta.FailedStep.LastError,
			"retry_ready": meta.FailedStep.RetryReady,
		}
	}
	return resp
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

// WithdrawalResponse is the 201 response body emitted by CreateWithdrawal.
// It surfaces the final broadcast tx alongside the sweep metadata so clients
// can see which strategy the planner chose and which intermediate legs ran.
type WithdrawalResponse struct {
	TransactionID string                  `json:"transaction_id" example:"8c5e3b3a-4a8f-4c0b-9e8a-2d1e8f0b7c4d"`
	TxHash        string                  `json:"tx_hash"        example:"0xaaa..."`
	Status        string                  `json:"status"         example:"confirming"`
	Origin        string                  `json:"origin"         example:"user_request"`
	Strategy      string                  `json:"strategy,omitempty" example:"direct_from_base"`
	Sweeps        []WithdrawalSweepItem   `json:"sweeps,omitempty"`
	FailedStep    *WithdrawalFailedStep   `json:"failed_step,omitempty"`
}

// WithdrawalSweepItem describes a single consolidation leg executed by the
// sweep planner before the final withdrawal broadcast.
type WithdrawalSweepItem struct {
	TxID   string `json:"tx_id"   example:"1f2d3c4b-5a6b-7c8d-9e0f-112233445566"`
	TxHash string `json:"tx_hash" example:"0xbbb..."`
	From   string `json:"from"    example:"0xChildAddress"`
	Origin string `json:"origin"  example:"sweep"`
}

// WithdrawalFailedStep records the mid-plan failure position when a sweep leg
// could not complete. Present only on partial-execution error responses.
type WithdrawalFailedStep struct {
	Index      int    `json:"index"       example:"1"`
	LastError  string `json:"last_error"  example:"insufficient gas"`
	RetryReady bool   `json:"retry_ready" example:"true"`
}
