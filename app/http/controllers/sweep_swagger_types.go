package controllers

// Swagger request/response types of the sweep routes (doc-only).

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
