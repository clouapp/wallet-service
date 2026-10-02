package controllers

import (
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/services/sweep"
)

// gasCheckRateLimitWindow caps ForceGasCheck to one on-chain read per wallet
// per minute. The limit is enforced via Redis SETNX so it is shared across all
// API nodes and Lambda handlers. When Redis is unavailable we fall through to
// the underlying handler — availability of the endpoint is more important than
// a perfect rate limit, and the chain adapter itself has short-term caching.
const gasCheckRateLimitWindow = 60 * time.Second

// ConsolidateWallet godoc
// @Summary      Consolidate wallet balances
// @Description  Sweep all eligible child addresses' balance into the wallet's base deposit address.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string                       true  "Wallet UUID"  format(uuid)
// @Param        body      body      ConsolidateRequestSwagger    true  "Consolidation request"
// @Success      200       {object}  ConsolidateResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Failure      429       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/consolidate [post]
func ConsolidateWallet(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.ConsolidateRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	callerAccountID, _ := ctx.Value("account_id").(uuid.UUID)

	result, err := container.Get().SweepService.ConsolidateAll(ctx.Context(), walletID, req.Asset, req.Passphrase, callerAccountID)
	if err != nil {
		if resp := MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return MapInternalError(ctx, err, "consolidate")
	}
	return ctx.Response().Success().Json(mapConsolidateResponse(result))
}

// GetGasStatus godoc
// @Summary      Get wallet gas readiness
// @Description  Returns the gas readiness status, native balance, and configured threshold for a wallet's base address.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string  true  "Wallet UUID"  format(uuid)
// @Success      200       {object}  GasStatusResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/gas-status [get]
func GetGasStatus(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	status, err := container.Get().SweepService.RefreshGasStatus(ctx.Context(), walletID)
	if err != nil {
		if resp := MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return MapInternalError(ctx, err, "gas_status")
	}
	return ctx.Response().Success().Json(gasStatusResponse(status))
}

// ForceGasCheck godoc
// @Summary      Force refresh of wallet gas status
// @Description  Action variant of gas-status. Forces an on-chain read. Rate-limited per wallet.
// @Tags         Wallets
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string  true  "Wallet UUID"  format(uuid)
// @Success      200       {object}  GasStatusResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Failure      429       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/gas-check [post]
func ForceGasCheck(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	if rdb := container.Get().Redis; rdb != nil {
		key := "vault:ratelimit:gas-check:" + walletID.String()
		ok, setErr := rdb.SetNX(ctx.Context(), key, "1", gasCheckRateLimitWindow).Result()
		if setErr == nil && !ok {
			return ctx.Response().Json(http.StatusTooManyRequests, http.Json{
				"error":               "rate_limited",
				"limit_type":          "gas_check",
				"retry_after_seconds": int(gasCheckRateLimitWindow / time.Second),
			})
		}
	}

	return GetGasStatus(ctx)
}

// PreviewWithdraw godoc
// @Summary      Preview a withdrawal
// @Description  Returns the planned sweep strategy without executing any transaction.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Param        walletId  path      string                            true  "Wallet UUID"  format(uuid)
// @Param        body      body      WithdrawPreviewRequestSwagger     true  "Preview request"
// @Success      200       {object}  WithdrawPreviewResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      422       {object}  ErrorResponse
// @Router       /v1/wallets/{walletId}/withdraw/preview [post]
func PreviewWithdraw(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid wallet id"})
	}

	var req requests.WithdrawPreviewRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid amount"})
	}

	callerAccountID, _ := ctx.Value("account_id").(uuid.UUID)

	const previewHasNoDestination = ""
	plan, err := container.Get().SweepService.PlanForWithdrawal(ctx.Context(), walletID, req.Asset, amount, previewHasNoDestination, callerAccountID)
	if err != nil {
		if resp := MapSweepError(ctx, err); resp != nil {
			return resp
		}
		return MapInternalError(ctx, err, "preview_withdraw")
	}
	return ctx.Response().Success().Json(previewResponse(plan))
}

// mapConsolidateResponse shapes a sweep.Result into the JSON payload the
// dashboard expects. CompletedSweep carries no amount today, so the
// aggregate total_amount is reported as "0" — the sweep legs themselves
// are the source of truth.
func mapConsolidateResponse(result *sweep.Result) http.Json {
	txs := make([]http.Json, 0, len(result.Sweeps))
	for _, sw := range result.Sweeps {
		txs = append(txs, http.Json{
			"tx_hash": sw.TxHash,
			"from":    sw.From.Address,
			"origin":  "manual_consolidation",
			"status":  "confirming",
		})
	}

	summary := http.Json{
		"children_swept":     len(result.Sweeps),
		"dust_ignored":       0,   // TODO: ConsolidateAll currently drops dust silently; surface when Plan is returned alongside Result.
		"total_amount":       "0", // TODO: CompletedSweep has no Amount field; aggregate when types expose it.
		"estimated_gas_cost": bigIntString(result.EstimatedGas),
	}

	body := http.Json{
		"plan_summary": summary,
		"transactions": txs,
	}
	if result.FailedStep != nil {
		body["failed_step"] = http.Json{
			"index":       result.FailedStep.Index,
			"last_error":  result.FailedStep.LastError,
			"retry_ready": result.FailedStep.RetryReady,
		}
	}
	return body
}

func gasStatusResponse(st *sweep.GasStatus) http.Json {
	body := http.Json{
		"gas_status":      st.Status,
		"base_address":    st.BaseAddress,
		"native_asset":    st.NativeAsset,
		"last_checked_at": st.LastCheckedAt,
	}
	if st.NativeBalance != nil {
		body["native_balance_raw"] = st.NativeBalance.String()
		// TODO: format native_balance_display via chain adapter's unit formatter when available.
		body["native_balance_display"] = st.NativeBalance.String()
	}
	if st.Threshold != nil {
		body["threshold_raw"] = st.Threshold.String()
		// TODO: format threshold_display via chain adapter's unit formatter when available.
		body["threshold_display"] = st.Threshold.String()
	}
	return body
}

func previewResponse(plan *sweep.Plan) http.Json {
	dustIgnored := make([]http.Json, 0, len(plan.DustIgnored))
	for _, d := range plan.DustIgnored {
		entry := http.Json{
			"address": d.Address.Address,
			"reason":  d.Reason,
		}
		if d.Balance != nil {
			entry["balance"] = d.Balance.String()
		} else {
			entry["balance"] = "0"
		}
		dustIgnored = append(dustIgnored, entry)
	}

	body := http.Json{
		"strategy":                   string(plan.Strategy),
		"reaches_target":             plan.ReachesTarget,
		"sweeps_required":            len(plan.Sweeps),
		"dust_ignored":               dustIgnored,
		"estimated_gas_total_native": bigIntString(plan.EstimatedGas),
	}
	if plan.BaseBalance != nil {
		body["base_balance"] = plan.BaseBalance.String()
	}
	return body
}

// bigIntString serialises a *big.Int for JSON responses. nil is normalised to
// "0" so clients never see a missing field, matching the prior hardcoded
// default. Callers that need to distinguish "estimate unavailable" from
// "estimate is exactly zero" should add a separate flag to the payload.
func bigIntString(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

// ---- Swagger request/response types (doc-only) ----

type ConsolidateRequestSwagger struct {
	Passphrase     string `json:"passphrase"                example:"my-secure-wallet-passphrase"`
	Asset          string `json:"asset"                     example:"eth"`
	IdempotencyKey string `json:"idempotency_key,omitempty" example:"cns_01HABCDEFG"`
}

type WithdrawPreviewRequestSwagger struct {
	Asset  string `json:"asset"  example:"eth"`
	Amount string `json:"amount" example:"1000000000000000000"`
}

type ConsolidateTransaction struct {
	TxHash string `json:"tx_hash"`
	From   string `json:"from"`
	Origin string `json:"origin" example:"manual_consolidation"`
	Status string `json:"status" example:"confirming"`
}

type ConsolidatePlanSummary struct {
	ChildrenSwept    int    `json:"children_swept"    example:"3"`
	DustIgnored      int    `json:"dust_ignored"      example:"0"`
	TotalAmount      string `json:"total_amount"      example:"0"`
	EstimatedGasCost string `json:"estimated_gas_cost" example:"0"`
}

type ConsolidateResponse struct {
	PlanSummary  ConsolidatePlanSummary   `json:"plan_summary"`
	Transactions []ConsolidateTransaction `json:"transactions"`
}

type GasStatusResponse struct {
	GasStatus            string `json:"gas_status"                       example:"seeded"`
	BaseAddress          string `json:"base_address"                     example:"0x..."`
	NativeAsset          string `json:"native_asset"                     example:"eth"`
	NativeBalanceRaw     string `json:"native_balance_raw,omitempty"     example:"1000000000000000000"`
	NativeBalanceDisplay string `json:"native_balance_display,omitempty" example:"1.0"`
	ThresholdRaw         string `json:"threshold_raw,omitempty"          example:"5000000000000000"`
	ThresholdDisplay     string `json:"threshold_display,omitempty"      example:"0.005"`
	LastCheckedAt        int64  `json:"last_checked_at"                  example:"1713500000"`
}

type WithdrawPreviewDustEntry struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
	Reason  string `json:"reason" example:"below_dust_threshold"`
}

type WithdrawPreviewResponse struct {
	Strategy                string                     `json:"strategy"                    example:"multi_sweep"`
	ReachesTarget           bool                       `json:"reaches_target"              example:"true"`
	BaseBalance             string                     `json:"base_balance,omitempty"`
	SweepsRequired          int                        `json:"sweeps_required"             example:"2"`
	DustIgnored             []WithdrawPreviewDustEntry `json:"dust_ignored"`
	EstimatedGasTotalNative string                     `json:"estimated_gas_total_native" example:"0"`
}
