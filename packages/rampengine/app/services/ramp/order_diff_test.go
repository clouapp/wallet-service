package ramp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestOrderSnapshotChangedIgnoresDecimalScale(t *testing.T) {
	before := &models.Transaction{AmountToken: "18.500000000000000000", Status: models.TxStatusProcessing}
	after := *before
	after.AmountToken = "18.500000"
	require.False(t, orderSnapshotChanged(before, &after))
}

func TestOrderSnapshotChangedDetectsStatusChange(t *testing.T) {
	before := &models.Transaction{Status: models.TxStatusProcessing}
	after := *before
	after.Status = models.TxStatusCompleted
	require.True(t, orderSnapshotChanged(before, &after))
}

func TestOrderSnapshotChangedIgnoresCanonicalJSONAndNumericScale(t *testing.T) {
	instructionsDB := `{"currency": "usdc", "amount": "18.5"}`
	instructionsProvider := `{"amount":"18.5","currency":"usdc"}`
	rawDB := `{"id": "trf_1", "state": "processing"}`
	feeDB := "0.277500000000000000"
	feeProvider := "0.277500"

	before := &models.Transaction{
		Status:              models.TxStatusProcessing,
		RawPayload:          rawDB,
		FeeAmount:           &feeDB,
		DepositInstructions: &instructionsDB,
	}
	after := *before
	after.RawPayload = `{"id":"trf_1","state":"processing"}`
	afterFee := feeProvider
	after.FeeAmount = &afterFee
	afterInstructions := instructionsProvider
	after.DepositInstructions = &afterInstructions

	require.False(t, orderSnapshotChanged(before, &after))
}

func TestOrderSnapshotChangedDetectsFeeChange(t *testing.T) {
	before := &models.Transaction{Status: models.TxStatusProcessing}
	fee := "1.00"
	after := *before
	afterFee := "2.00"
	after.FeeAmount = &afterFee
	before.FeeAmount = &fee
	require.True(t, orderSnapshotChanged(before, &after))
}

// A deposit that fails, or one that arrives with the payer's name and
// reference for the first time, changes nothing money-shaped — same status,
// same amounts — and would otherwise be read as unchanged and never persisted.
func TestOrderSnapshotChangedDetectsFailureReasonAndSenderFields(t *testing.T) {
	before := &models.Transaction{Status: models.TxStatusFundsReceived}

	failed := *before
	reason := "insufficient_funds"
	failed.FailureReason = &reason
	require.True(t, orderSnapshotChanged(before, &failed))

	named := *before
	name := "Luiza Souza"
	named.SenderName = &name
	require.True(t, orderSnapshotChanged(before, &named))

	referenced := *before
	ref := "E12345678202609141200s00123456789"
	referenced.SenderReference = &ref
	require.True(t, orderSnapshotChanged(before, &referenced))
}

func ptr(s string) *string { return &s }

// A field the diff misses is a provider change the sweep never persists.
func TestOrderSnapshotChangedDetectsEachProviderField(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Minute)
	cases := map[string]func(*models.Transaction){
		"token amount":         func(tx *models.Transaction) { tx.AmountToken = "18.23" },
		"exchange fee":         func(tx *models.Transaction) { tx.ExchangeFeeAmount = ptr("0.21") },
		"fee currency":         func(tx *models.Transaction) { tx.FeeCurrency = ptr("usd") },
		"tx hash":              func(tx *models.Transaction) { tx.DepositTxSignature = ptr("0xdef") },
		"deposit instructions": func(tx *models.Transaction) { tx.DepositInstructions = ptr(`{"amount":"18.6"}`) },
		"malformed instructions": func(tx *models.Transaction) {
			tx.DepositInstructions = ptr(`{"amount":`)
		},
		"cleared instructions": func(tx *models.Transaction) { tx.DepositInstructions = nil },
		"expiry":               func(tx *models.Transaction) { tx.ExpiresAt = &later },
		"completion":           func(tx *models.Transaction) { tx.CompletedAt = &now },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			before := &models.Transaction{
				Status: models.TxStatusProcessing, AmountToken: "18.22",
				ExchangeFeeAmount: ptr("0.20"), FeeCurrency: ptr("brl"),
				DepositTxSignature: ptr("0xabc"), DepositInstructions: ptr(`{"amount":"18.5"}`),
				ExpiresAt: &now,
			}
			after := *before
			change(&after)

			require.True(t, orderSnapshotChanged(before, &after))
		})
	}
}

func strPtr(s string) *string { return &s }
