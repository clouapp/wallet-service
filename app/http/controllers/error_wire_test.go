package controllers

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/feeestimate"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
)

// legacyJSON and envelopeJSON are the two error writers on the wire today:
// ctx.Response().Json (gin, no trailing newline) and responses.JSON
// (encoding/json Encoder, trailing newline).
const (
	legacyJSON   = "application/json; charset=utf-8"
	envelopeJSON = "application/json"
)

// TestError_Mappers_KeepTheirBytes pins, byte for byte, the error answers
// the contract scenario cannot reach without a chain call or a failing store:
// both 500 codes, the 502 writer, and the sweep and spending-limit domain codes.
func TestError_Mappers_KeepTheirBytes(t *testing.T) {
	cause := errors.New("dial tcp 10.0.0.7:5432: connect: connection refused")
	cases := []struct {
		name        string
		answer      func(http.Context) http.Response
		status      int
		contentType string
		body        string
	}{
		{"MapInternalError", func(ctx http.Context) http.Response { return MapInternalError(ctx, cause, "contract") },
			500, legacyJSON, `{"error":{"code":"internal_error","message":"internal_error"}}`},
		{"a 500 refusal sentence", func(ctx http.Context) http.Response {
			return MapWithdrawalCreateError(ctx, &withdraw.CreateRefusal{Status: withdraw.CreateStatusInternal, Message: "failed to persist withdrawal"})
		}, 500, legacyJSON, `{"error":{"code":"internal","message":"failed to persist withdrawal"}}`},
		{"an unknown asset on create", func(ctx http.Context) http.Response {
			return MapWithdrawalCreateError(ctx, &withdraw.CreateRefusal{Status: withdraw.CreateStatusBadRequest, Message: "unknown asset ZZZ on eth"})
		}, 400, legacyJSON, `{"error":{"code":"invalid_request","message":"unknown asset"}}`},
		{"a refused create", func(ctx http.Context) http.Response {
			return MapWithdrawalCreateError(ctx, &withdraw.CreateRefusal{Status: withdraw.CreateStatusForbidden, Message: "2FA must be enabled before withdrawing"})
		}, 403, legacyJSON, `{"error":{"code":"forbidden","message":"2FA must be enabled before withdrawing"}}`},
		{"responses.InternalError", func(ctx http.Context) http.Response { return responses.InternalError(ctx, cause) },
			500, envelopeJSON, `{"error":{"code":"internal","message":"internal error"}}` + "\n"},
		{"responses.ProviderError", func(ctx http.Context) http.Response { return responses.ProviderError(ctx, cause) },
			502, envelopeJSON, `{"error":{"code":"provider_unavailable","message":"provider unavailable"}}` + "\n"},
		{"AddressGenerationError", func(ctx http.Context) http.Response { return AddressGenerationError(ctx, cause) },
			500, envelopeJSON, `{"error":{"code":"internal","message":"internal error"}}` + "\n"},
		{"in-flight consolidation", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrInFlightConsolidation) },
			429, legacyJSON, `{"error":{"code":"sweep_limit_exceeded","limit_type":"in_flight_consolidation","message":"sweep_limit_exceeded","retry_after_seconds":60}}`},
		{"daily quota", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrDailyQuotaExceeded) },
			429, legacyJSON, `{"error":{"code":"sweep_limit_exceeded","limit_type":"daily_quota","message":"sweep_limit_exceeded"}}`},
		{"addresses per request", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrTooManyAddresses) },
			429, legacyJSON, `{"error":{"code":"sweep_limit_exceeded","limit_type":"addresses_per_request","message":"sweep_limit_exceeded"}}`},
		{"not gas ready", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrWalletNotGasReady) },
			422, legacyJSON, `{"error":{"action":"fund_base_address","code":"wallet_not_gas_ready","message":"wallet_not_gas_ready"}}`},
		{"insufficient funds", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrInsufficientFunds) },
			422, legacyJSON, `{"error":{"code":"insufficient_funds","message":"insufficient_funds"}}`},
		{"unsupported chain", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrUnsupportedChain) },
			422, legacyJSON, `{"error":{"code":"unsupported_chain","message":"unsupported_chain"}}`},
		{"gas estimate failed", func(ctx http.Context) http.Response { return MapSweepError(ctx, sweep.ErrGasEstimateFailed) },
			422, legacyJSON, `{"error":{"code":"gas_estimate_failed","message":"gas_estimate_failed"}}`},
		{"spending limit exceeded", func(ctx http.Context) http.Response {
			return MapSpendingLimitError(ctx, withdraw.ErrSpendingLimitExceeded)
		},
			429, legacyJSON, `{"error":{"code":"spending_limit_exceeded","limit_type":"daily_usd","message":"spending_limit_exceeded"}}`},
		{"spending limit invalid", func(ctx http.Context) http.Response {
			return MapSpendingLimitError(ctx, withdraw.ErrSpendingLimitInvalid)
		},
			422, legacyJSON, `{"error":{"code":"spending_limit_invalid","message":"spending_limit_invalid"}}`},
		{"spending quote unavailable", func(ctx http.Context) http.Response {
			return MapSpendingLimitError(ctx, withdraw.ErrSpendingQuoteUnavailable)
		},
			503, legacyJSON, `{"error":{"code":"spending_limit_quote_unavailable","message":"spending_limit_quote_unavailable"}}`},
		{"a fee estimate refusal keeps its own code", func(ctx http.Context) http.Response {
			return feeEstimateErrorResponse(ctx, &models.Wallet{}, &feeestimate.Error{
				Kind: feeestimate.KindUnprocessable, Code: feeestimate.CodeUnknownAsset, Message: "unknown asset ZZZ",
			})
		}, 422, legacyJSON, `{"error":{"code":"unknown_asset","message":"unknown asset"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(rec)
			ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", nil)
			response := tc.answer(ginpkg.NewContext(ginCtx))
			if response == nil {
				t.Fatal("no response")
			}
			if err := response.Render(); err != nil {
				t.Fatalf("render: %v", err)
			}
			rec.Flush()
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("content type = %q, want %q", got, tc.contentType)
			}
			if got := rec.Body.String(); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}
