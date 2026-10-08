package controllers

import (
	"math/big"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/services/sweep"
)

// mapConsolidateResponse shapes a sweep.Result into the JSON payload the
// dashboard expects. total_amount is the sum, in base units, of what the
// broadcast legs moved.
func mapConsolidateResponse(result *sweep.Result) http.Json {
	txs := make([]http.Json, 0, len(result.Sweeps))
	total := new(big.Int)
	for _, sw := range result.Sweeps {
		txs = append(txs, http.Json{
			"tx_hash": sw.TxHash,
			"from":    sw.From.Address,
			"amount":  bigIntString(sw.Amount),
			"origin":  "manual_consolidation",
			"status":  "confirming",
		})
		if sw.Amount != nil {
			total.Add(total, sw.Amount)
		}
	}

	summary := http.Json{
		"children_swept":     len(result.Sweeps),
		"dust_ignored":       0, // TODO: ConsolidateAll currently drops dust silently; surface when Plan is returned alongside Result.
		"total_amount":       total.String(),
		"estimated_gas_cost": bigIntString(result.EstimatedGas),
	}
	if result.AssetDecimals != nil {
		summary["decimals"] = *result.AssetDecimals
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

// MapConsolidateResponse, GasStatusBody and WithdrawPreviewBody are the sweep JSON
// both HTTP surfaces return.
func MapConsolidateResponse(result *sweep.Result) http.Json {
	return mapConsolidateResponse(result)
}

func GasStatusBody(st *sweep.GasStatus) http.Json {
	return gasStatusResponse(st)
}

func WithdrawPreviewBody(plan *sweep.Plan) http.Json {
	return previewResponse(plan)
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
	Amount string `json:"amount" example:"7000000"`
	Origin string `json:"origin" example:"manual_consolidation"`
	Status string `json:"status" example:"confirming"`
}

type ConsolidatePlanSummary struct {
	ChildrenSwept    int    `json:"children_swept"    example:"3"`
	DustIgnored      int    `json:"dust_ignored"      example:"0"`
	TotalAmount      string `json:"total_amount"      example:"7000000"`
	Decimals         int    `json:"decimals,omitempty" example:"6"`
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
