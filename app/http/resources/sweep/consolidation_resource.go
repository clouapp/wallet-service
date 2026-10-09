package sweep

import (
	"math/big"

	"github.com/macrowallets/waas/app/services/sweep"
)

// Consolidation is the answer to a consolidation, on both HTTP surfaces. The
// fields are in the order the JSON object has always been written (sorted by
// key). FailedStep is only on the wire when a leg failed.
type Consolidation struct {
	FailedStep   *ConsolidationFailedStep `json:"failed_step,omitempty"`
	PlanSummary  ConsolidationSummary     `json:"plan_summary"`
	Transactions []ConsolidationLeg       `json:"transactions"`
}

// ConsolidationSummary totals the legs that broadcast. Decimals is only on the
// wire when the swept asset's decimals are known.
type ConsolidationSummary struct {
	ChildrenSwept int  `json:"children_swept"`
	Decimals      *int `json:"decimals,omitempty"`
	// DustIgnored is always 0: ConsolidateAll drops dust silently until the plan
	// is returned beside the result.
	DustIgnored      int    `json:"dust_ignored"`
	EstimatedGasCost string `json:"estimated_gas_cost"`
	TotalAmount      string `json:"total_amount"`
}

// ConsolidationLeg is one sweep that broadcast.
type ConsolidationLeg struct {
	Amount string `json:"amount"`
	From   string `json:"from"`
	Origin string `json:"origin"`
	Status string `json:"status"`
	TxHash string `json:"tx_hash"`
}

// ConsolidationFailedStep is the leg a failed consolidation stopped at.
type ConsolidationFailedStep struct {
	Index      int    `json:"index"`
	LastError  string `json:"last_error"`
	RetryReady bool   `json:"retry_ready"`
}

// NewConsolidation shapes a sweep result. total_amount is the sum, in base
// units, of what the broadcast legs moved.
func NewConsolidation(result *sweep.Result) Consolidation {
	legs := make([]ConsolidationLeg, 0, len(result.Sweeps))
	total := new(big.Int)
	for _, swept := range result.Sweeps {
		legs = append(legs, ConsolidationLeg{
			Amount: bigIntString(swept.Amount),
			From:   swept.From.Address,
			Origin: "manual_consolidation",
			Status: "confirming",
			TxHash: swept.TxHash,
		})
		if swept.Amount != nil {
			total.Add(total, swept.Amount)
		}
	}

	view := Consolidation{
		PlanSummary: ConsolidationSummary{
			ChildrenSwept:    len(result.Sweeps),
			Decimals:         result.AssetDecimals,
			EstimatedGasCost: bigIntString(result.EstimatedGas),
			TotalAmount:      total.String(),
		},
		Transactions: legs,
	}
	if result.FailedStep != nil {
		view.FailedStep = &ConsolidationFailedStep{
			Index:      result.FailedStep.Index,
			LastError:  result.FailedStep.LastError,
			RetryReady: result.FailedStep.RetryReady,
		}
	}
	return view
}

// bigIntString writes a base-unit amount. A nil amount is "0", so a client
// never sees a missing field.
func bigIntString(value *big.Int) string {
	if value == nil {
		return "0"
	}
	return value.String()
}
