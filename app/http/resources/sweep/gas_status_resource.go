package sweep

import "github.com/macrowallets/waas/app/services/sweep"

// GasStatus is a wallet's gas readiness, on both HTTP surfaces. The fields are
// in the order the JSON object has always been written (sorted by key). The
// balance and threshold are only on the wire when they were read.
type GasStatus struct {
	BaseAddress   string `json:"base_address"`
	GasStatus     string `json:"gas_status"`
	LastCheckedAt int64  `json:"last_checked_at"`
	NativeAsset   string `json:"native_asset"`
	// The display values repeat the raw ones until the chain adapter formats units.
	NativeBalanceDisplay *string `json:"native_balance_display,omitempty"`
	NativeBalanceRaw     *string `json:"native_balance_raw,omitempty"`
	ThresholdDisplay     *string `json:"threshold_display,omitempty"`
	ThresholdRaw         *string `json:"threshold_raw,omitempty"`
}

// NewGasStatus shapes a gas-readiness snapshot.
func NewGasStatus(status *sweep.GasStatus) GasStatus {
	view := GasStatus{
		BaseAddress:   status.BaseAddress,
		GasStatus:     status.Status,
		LastCheckedAt: status.LastCheckedAt,
		NativeAsset:   status.NativeAsset,
	}
	if status.NativeBalance != nil {
		balance := status.NativeBalance.String()
		view.NativeBalanceRaw, view.NativeBalanceDisplay = &balance, &balance
	}
	if status.Threshold != nil {
		threshold := status.Threshold.String()
		view.ThresholdRaw, view.ThresholdDisplay = &threshold, &threshold
	}
	return view
}
