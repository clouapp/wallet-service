package sweep

import "github.com/macrowallets/waas/app/services/sweep"

// Preview is the planned strategy of a withdrawal that nothing executed. The
// fields are in the order the JSON object has always been written (sorted by
// key). BaseBalance is only on the wire when the base balance was read.
type Preview struct {
	BaseBalance             *string       `json:"base_balance,omitempty"`
	DustIgnored             []PreviewDust `json:"dust_ignored"`
	EstimatedGasTotalNative string        `json:"estimated_gas_total_native"`
	ReachesTarget           bool          `json:"reaches_target"`
	Strategy                string        `json:"strategy"`
	SweepsRequired          int           `json:"sweeps_required"`
}

// PreviewDust is an address the plan left out because its balance is dust.
type PreviewDust struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
	Reason  string `json:"reason"`
}

// NewPreview shapes a withdrawal plan.
func NewPreview(plan *sweep.Plan) Preview {
	dust := make([]PreviewDust, 0, len(plan.DustIgnored))
	for _, ignored := range plan.DustIgnored {
		balance := "0"
		if ignored.Balance != nil {
			balance = ignored.Balance.String()
		}
		dust = append(dust, PreviewDust{Address: ignored.Address.Address, Balance: balance, Reason: ignored.Reason})
	}

	view := Preview{
		DustIgnored:             dust,
		EstimatedGasTotalNative: bigIntString(plan.EstimatedGas),
		ReachesTarget:           plan.ReachesTarget,
		Strategy:                string(plan.Strategy),
		SweepsRequired:          len(plan.Sweeps),
	}
	if plan.BaseBalance != nil {
		balance := plan.BaseBalance.String()
		view.BaseBalance = &balance
	}
	return view
}
