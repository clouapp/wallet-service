package ramp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The adapters map ProviderOrderID straight off the response with no guard, so
// a 200 carrying no id arrives here empty. Blanking the column would drop the
// order out of reconciliation for good, with no error and no log:
// Reconciler.refresh persists the blank, and the next sweep returns early on
// `GatewayOrderID == ""`.
func TestApplyOrderKeepsTheGatewayOrderIDWhenTheProviderNamesNone(t *testing.T) {
	tx := &models.Transaction{GatewayOrderID: "trf_1", Status: models.TxStatusProcessing}

	ApplyOrder(tx, &ramp.RampOrder{Status: models.TxStatusCompleted})

	require.Equal(t, "trf_1", tx.GatewayOrderID, "an absent field means the provider said nothing")
	require.Equal(t, models.TxStatusCompleted, tx.Status)
}

func TestApplyOrderTakesTheGatewayOrderIDWhenTheProviderNamesOne(t *testing.T) {
	tx := &models.Transaction{GatewayOrderID: "trf_1"}

	ApplyOrder(tx, &ramp.RampOrder{ProviderOrderID: "trf_2"})

	require.Equal(t, "trf_2", tx.GatewayOrderID)
}

// Bridge's receipt names its conversion cut ("exchange_fee": 0.06 USDC on a
// 10 USDC off-ramp in production, 01/10). The column existed and stayed empty
// for every transfer, so the app could only show XIP's 0% beside a payout
// 0.55% under the market.
func TestApplyOrderRecordsTheProvidersExchangeFee(t *testing.T) {
	tx := &models.Transaction{Type: models.TransactionTypeOfframp}

	ApplyOrder(tx, &ramp.RampOrder{Amounts: map[string]string{"exchange_fee": "0.06", "destination_amount": "51.97"}})

	require.NotNil(t, tx.ExchangeFeeAmount)
	require.Equal(t, "0.06", *tx.ExchangeFeeAmount)
	require.Equal(t, "51.97", tx.AmountFiat)
}

// A bank payout's settled figure is the dollars at the bank, written to the
// order's fiat leg in its own currency.
func TestApplyOrderOfABankRailWritesTheSettledAmountToAmountFiat(t *testing.T) {
	for _, rail := range models.BankRails() {
		tx := &models.Transaction{
			Type: models.TransactionTypeOfframp, Rail: &rail, AmountFiat: "0", FiatCurrency: models.FiatCurrencyUSD,
			AmountToken: "10.00",
		}

		ApplyOrder(tx, &ramp.RampOrder{Amounts: map[string]string{"destination_amount": "98.5", "source_amount": "10.25"}})

		require.Equal(t, "98.5", tx.AmountFiat, rail)
		require.Equal(t, models.FiatCurrencyUSD, tx.FiatCurrency, rail)
		require.Equal(t, "10.25", tx.AmountToken, rail)
	}
}

// A bank payout's failure reason reaches the row whichever path read it: the
// sandbox's refresh of a failed payout left failure_reason null while only the
// webhook copied it (finding 3).
func TestApplyOrderOfABankRailRecordsTheProvidersFailureReason(t *testing.T) {
	for _, rail := range models.BankRails() {
		tx := &models.Transaction{Type: models.TransactionTypeOfframp, Rail: &rail, Status: models.TxStatusProcessing}
		order := &ramp.RampOrder{Status: models.TxStatusFailed, FailureReason: "solana_delegate_approval_missing"}

		require.True(t, OrderChangedFromProvider(tx, order), rail)
		ApplyOrder(tx, order)

		require.NotNil(t, tx.FailureReason, rail)
		require.Equal(t, "solana_delegate_approval_missing", *tx.FailureReason, rail)
	}
}

func TestApplyOrderOfABankRailKeepsTheReasonWhenTheProviderNamesNone(t *testing.T) {
	rail := models.RailACHPayout
	reason := "solana_insufficient_token_balance"
	tx := &models.Transaction{Type: models.TransactionTypeOfframp, Rail: &rail, Status: models.TxStatusFailed, FailureReason: &reason}

	ApplyOrder(tx, &ramp.RampOrder{Status: models.TxStatusFailed})

	require.NotNil(t, tx.FailureReason)
	require.Equal(t, reason, *tx.FailureReason)
}

// Other rails keep their own failure bookkeeping (a two-phase on-ramp's
// delivery records its reason with its own prefix).
func TestApplyOrderOfAnotherRailLeavesTheFailureReason(t *testing.T) {
	rail := models.RailPixPayout
	tx := &models.Transaction{Type: models.TransactionTypeOfframp, Rail: &rail}

	ApplyOrder(tx, &ramp.RampOrder{Status: models.TxStatusFailed, FailureReason: "rejected"})

	require.Nil(t, tx.FailureReason)
}
