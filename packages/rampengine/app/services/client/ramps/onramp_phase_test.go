package ramps_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

func TestDeriveOnrampPhase(t *testing.T) {
	t.Parallel()
	sig := "abc123"
	cases := []struct {
		name string
		tx   *models.Transaction
		want string
	}{
		{"offramp", &models.Transaction{Type: models.TransactionTypeOfframp}, ""},
		{"awaiting", &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusAwaitingFunds}, "awaiting_pix"},
		{"pix", &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusFundsReceived}, "pix_settled"},
		{"delivering", &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusProcessing}, "delivering"},
		{"credited", &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusCompleted, DepositTxSignature: &sig}, "credited"},
		{"failed", &models.Transaction{Type: models.TransactionTypeOnramp, Status: models.TxStatusFailed}, "failed"},
		{"delivery exhausted", &models.Transaction{
			Type: models.TransactionTypeOnramp, Provider: models.ProviderOzav,
			Status: models.TxStatusProcessing, DeliveryAttempts: 3,
		}, "failed"},
		// A dollar deposit reads the same phases under names without Pix in
		// them; an installed build ignores a name it does not know and falls
		// back to the status, which says the same.
		{"usd awaiting", &models.Transaction{
			Type: models.TransactionTypeOnramp, FiatCurrency: models.FiatCurrencyUSD, Status: models.TxStatusAwaitingFunds,
		}, "awaiting_funds"},
		{"usd settled", &models.Transaction{
			Type: models.TransactionTypeOnramp, FiatCurrency: models.FiatCurrencyUSD, Status: models.TxStatusFundsReceived,
		}, "funds_settled"},
		{"usd delivering", &models.Transaction{
			Type: models.TransactionTypeOnramp, FiatCurrency: models.FiatCurrencyUSD, Status: models.TxStatusProcessing,
		}, "delivering"},
		{"brl explicit", &models.Transaction{
			Type: models.TransactionTypeOnramp, FiatCurrency: models.FiatCurrencyBRL, Status: models.TxStatusFundsReceived,
		}, "pix_settled"},
		{"credited on the last attempt", &models.Transaction{
			Type: models.TransactionTypeOnramp, Provider: models.ProviderOzav,
			Status: models.TxStatusCompleted, DeliveryAttempts: 3, DepositTxSignature: &sig,
		}, "credited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ramps.DeriveOnrampPhase(tc.tx, 3))
		})
	}
}
