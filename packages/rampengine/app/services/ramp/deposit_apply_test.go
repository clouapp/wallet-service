package ramp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// An activity that carries no amount leaves the amounts it does not name alone.
func TestApplyDepositRecordsAFailureWithoutTouchingTheAmounts(t *testing.T) {
	tx := &models.Transaction{Status: models.TxStatusFundsReceived, AmountFiat: "100.00", AmountToken: "18.22"}

	ApplyDeposit(tx, &models.CustomerVirtualAccount{SourceCurrency: "brl"}, &ramp.VirtualAccountDeposit{
		Status: models.TxStatusFailed, FailureReason: "returned", SenderName: "Ana Souza", SenderReference: "E123",
	})

	require.Equal(t, models.TxStatusFailed, tx.Status)
	require.Equal(t, "returned", *tx.FailureReason)
	require.Equal(t, "Ana Souza", *tx.SenderName)
	require.Equal(t, "E123", *tx.SenderReference)
	require.Equal(t, "100.00", tx.AmountFiat)
	require.Equal(t, "18.22", tx.AmountToken)
	require.Equal(t, "brl", *tx.FeeCurrency)
	require.Nil(t, tx.CompletedAt)
}

// The fiat currency and the bank rail come onto the row: the currency from the
// account, never from the event; the rail from the deposit when it names one.
func TestApplyDepositRecordsTheFiatCurrencyAndTheRail(t *testing.T) {
	tx := &models.Transaction{Status: models.TxStatusPending}

	ApplyDeposit(tx, &models.CustomerVirtualAccount{SourceCurrency: "usd"}, &ramp.VirtualAccountDeposit{
		Status: models.TxStatusFundsReceived, SourceAmount: "120.0", PaymentRail: "wire",
	})

	require.Equal(t, "usd", tx.FiatCurrency)
	require.Equal(t, "usd", *tx.FeeCurrency)
	require.Equal(t, "wire", *tx.PaymentRail)
	require.Equal(t, "120.0", tx.AmountFiat)

	// A later activity that names no rail leaves the recorded one alone.
	ApplyDeposit(tx, &models.CustomerVirtualAccount{SourceCurrency: "usd"}, &ramp.VirtualAccountDeposit{
		Status: models.TxStatusCompleted,
	})
	require.Equal(t, "wire", *tx.PaymentRail)
}
