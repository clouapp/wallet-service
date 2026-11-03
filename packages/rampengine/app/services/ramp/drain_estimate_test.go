package ramp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// Until Bridge says what it sent, a payout shows the USD it is expected to
// send: the fee Bridge reported wins over the address's percent, and the
// receipt replaces the estimate.
func TestApplyLiquidationDrainEstimatesTheFiatLeg(t *testing.T) {
	address := liquidationAddressRow()

	tx := &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_2", Status: models.TxStatusFundsReceived, SourceAmount: "80", FeeAmount: "1.20",
	})
	require.Equal(t, "78.80", tx.AmountFiat)

	tx = &models.Transaction{}
	rampsvc.ApplyLiquidationDrain(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_3", Status: models.TxStatusFundsReceived, SourceAmount: "33.333",
	})
	require.Equal(t, "32.99", tx.AmountFiat, "1% off, rounded down to cents")

	rampsvc.ApplyLiquidationDrain(tx, address, &ramp.LiquidationDrain{
		DrainID: "drn_3", Status: models.TxStatusCompleted, DestinationAmount: "33.00",
	})
	require.Equal(t, "33.00", tx.AmountFiat, "the receipt replaces the estimate")
}
