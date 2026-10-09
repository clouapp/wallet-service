package controllers

import (
	"errors"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// MapWithdrawalError maps a failure of withdraw.Service.Submit. Both HTTP
// surfaces create withdrawals and must answer the same bytes, so the mapping
// is shared; each surface's mapError delegates here. It returns nil for an
// error Submit does not produce.
//
// The body of a refusal stays the legacy {"error": message} map, which the
// writer wraps into {"error":{"code","message"}}. A row failure is a generic 500.
func MapWithdrawalError(ctx http.Context, err error) http.Response {
	var executed *withdraw.ExecuteError
	if errors.As(err, &executed) {
		return mapWithdrawalExecuteError(ctx, executed.Err)
	}
	if errors.Is(err, withdraw.ErrUnauthenticated) {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}
	// A chain the registry does not know is an outage, not something the
	// caller can fix, so its text (the refusal message names it) is not returned.
	if errors.Is(err, chainregistry.ErrUnknownChain) {
		slog.Error("create withdrawal chain unavailable")
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodeInternal, "internal error")
	}
	var refusal *withdraw.CreateRefusal
	if errors.As(err, &refusal) && refusal != nil {
		return mapWithdrawalRefusal(ctx, refusal)
	}
	var row *withdraw.CreateRowError
	if errors.As(err, &row) && row != nil {
		cause := row.Err
		if cause == nil {
			cause = err
		}
		endpoint := row.Endpoint
		if endpoint == "" {
			endpoint = "create_wallet_withdrawal"
		}
		return MapInternalError(ctx, cause, endpoint)
	}
	return nil
}

// mapWithdrawalExecuteError answers a failure of the signing and broadcast
// step, after the withdrawal row was marked failed.
func mapWithdrawalExecuteError(ctx http.Context, err error) http.Response {
	if resp := MapSpendingLimitError(ctx, err); resp != nil {
		return resp
	}
	if resp := MapSweepError(ctx, err); resp != nil {
		return resp
	}
	switch {
	case errors.Is(err, withdraw.ErrInvalidPassphrase):
		return responses.Error(ctx, http.StatusUnauthorized, responses.CodeUnauthorized, withdraw.ErrInvalidPassphrase.Error())
	case errors.Is(err, withdraw.ErrPassphraseTooShort):
		return responses.Error(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, withdraw.ErrPassphraseTooShort.Error())
	case errors.Is(err, withdraw.ErrInsufficientFunds):
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodeUnprocessable, withdraw.ErrInsufficientFunds.Error())
	case errors.Is(err, withdraw.ErrConcurrentWithdraw):
		return responses.Error(ctx, http.StatusConflict, responses.CodeConflict, withdraw.ErrConcurrentWithdraw.Error())
	case errors.Is(err, withdraw.ErrTooManyAttempts):
		return responses.Error(ctx, http.StatusTooManyRequests, responses.CodeTooManyRequests, withdraw.ErrTooManyAttempts.Error())
	default:
		return MapInternalError(ctx, err, "create_wallet_withdrawal")
	}
}

// mapWithdrawalRefusal keeps a caller-fixable refusal on the status and the
// sentence the service chose. withdraw.Service words every refusal for the
// client (an unknown asset is the fixed "unknown asset", not what was typed).
func mapWithdrawalRefusal(ctx http.Context, refusal *withdraw.CreateRefusal) http.Response {
	return responses.FailMessage(ctx, createRefusalStatus(refusal.Status), refusal.Message)
}

func createRefusalStatus(status withdraw.CreateStatus) int {
	switch status {
	case withdraw.CreateStatusUnauthorized:
		return http.StatusUnauthorized
	case withdraw.CreateStatusForbidden:
		return http.StatusForbidden
	case withdraw.CreateStatusBadRequest:
		return http.StatusBadRequest
	case withdraw.CreateStatusUnprocessable:
		return http.StatusUnprocessableEntity
	case withdraw.CreateStatusTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
