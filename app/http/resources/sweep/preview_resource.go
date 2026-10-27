package sweep

import "github.com/macrowallets/waas/app/services/sweep"

// Preview is the planned strategy of a withdrawal that nothing executed. The
// fields are in the order the JSON object has always been written (sorted by
// key). BaseBalance is only on the wire when the base balance was read.
type Preview struct {
	BaseBalance             *string                `json:"base_balance,omitempty"`
	DustIgnored             []PreviewDust          `json:"dust_ignored"`
	EstimatedGasTotalNative string                 `json:"estimated_gas_total_native"`
	ReachesTarget           bool                   `json:"reaches_target"`
	Strategy                string                 `json:"strategy"`
	SweepsRequired          int                    `json:"sweeps_required"`
	GasPreparation          *PreviewGasPreparation `json:"gas_preparation,omitempty"`
}

// PreviewGasPreparation is an explicit native transfer that must be observed
// before the quoted direct child can sign its token payment.
type PreviewGasPreparation struct {
	From                 string `json:"from"`
	To                   string `json:"to"`
	Chain                string `json:"chain"`
	AmountWei            string `json:"amount_wei"`
	MaxFeeWei            string `json:"max_fee_wei"`
	MinFunderBalanceWei  string `json:"min_funder_balance_wei"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
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
	if plan.GasPreparation != nil && plan.GasPreparationBinding != nil {
		p, b := plan.GasPreparation, plan.GasPreparationBinding
		view.GasPreparation = &PreviewGasPreparation{From: p.BaseAddress, To: b.SourceAddress, Chain: b.Chain, AmountWei: p.AmountWei,
			MaxFeeWei: p.MaxFeeWei, MinFunderBalanceWei: p.MinFunderBalanceWei, RequiresConfirmation: true}
	}
	return view
}
