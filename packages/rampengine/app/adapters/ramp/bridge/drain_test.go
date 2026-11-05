package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// drainObject is a fixture's event_object: a drain as the history lists it.
func drainObject(t *testing.T, fixture string) string {
	t.Helper()
	var envelope struct {
		EventObject json.RawMessage `json:"event_object"`
	}
	require.NoError(t, json.Unmarshal(readFixture(t, fixture), &envelope))
	return string(envelope.EventObject)
}

// The history is filtered by drain_id upstream, and the drain is still picked
// by id: a page that answers with a neighbour must not settle the wrong order.
func TestGetDrainReadsTheDrainOutOfTheHistory(t *testing.T) {
	other := strings.ReplaceAll(drainObject(t, "la_drain_funds_received.json"), "drn_6b2c3d4e5f6a7b8c", "drn_other")
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1/drains": listBody(other,
			drainObject(t, "la_drain_payment_processed.json")),
	})

	drain, err := adapter.GetDrain(context.Background(), bridgeCustomer, "la_1", "drn_6b2c3d4e5f6a7b8c")
	require.NoError(t, err)

	require.Equal(t, "drain_id=drn_6b2c3d4e5f6a7b8c&limit=10", (*calls)[0].query)
	require.Equal(t, "drn_6b2c3d4e5f6a7b8c", drain.DrainID)
	require.Equal(t, "la_1", drain.ProviderAddressID)
	require.Equal(t, models.TxStatusCompleted, drain.Status)
	require.Equal(t, "250.00", drain.SourceAmount)
	require.Equal(t, "usdc", drain.SourceCurrency)
	require.Equal(t, "247.50", drain.DestinationAmount)
	require.Equal(t, "2.50", drain.FeeAmount)
	require.Equal(t, "ach", drain.PaymentRail)
	require.Equal(t, "5xQyFundsReceivedSolanaSignature", drain.DepositTxHash)
	require.Equal(t, solanaAddress, drain.FromAddress)
	require.Equal(t, "718268532664263", drain.BankReference)
	require.Empty(t, drain.FailureReason)
	require.NotNil(t, drain.OccurredAt)
	require.True(t, drain.OccurredAt.Equal(time.Date(2026, 10, 15, 8, 31, 0, 0, time.UTC)))
	require.NotEmpty(t, drain.Raw)
}

// The docs show the history both wrapped in {data: [...]} and as a bare
// array; either is read.
func TestGetDrainReadsABareArray(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1/drains": "[" + drainObject(t, "la_drain_funds_received.json") + "]",
	})

	drain, err := adapter.GetDrain(context.Background(), bridgeCustomer, "la_1", "drn_6b2c3d4e5f6a7b8c")
	require.NoError(t, err)
	require.Equal(t, models.TxStatusFundsReceived, drain.Status)
	require.Equal(t, "250.00", drain.SourceAmount)
	require.Empty(t, drain.DestinationAmount, "no receipt yet: nothing was sent")
}

// Reporting an empty drain would let the reconciler blank an order it never saw.
func TestGetDrainRefusesADrainItCannotFind(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /customers/cus_1/liquidation_addresses/la_1/drains": listBody(),
	})

	_, err := adapter.GetDrain(context.Background(), bridgeCustomer, "la_1", "drn_x")
	require.EqualError(t, err, "bridge: drain drn_x is not in the history of liquidation address la_1")
}

// The table of spec D11. A state Bridge adds later stays in flight.
func TestDrainStatusFromBridge(t *testing.T) {
	for state, want := range map[string]string{
		"awaiting_funds":        models.TxStatusAwaitingFunds,
		"funds_received":        models.TxStatusFundsReceived,
		"payment_validated":     models.TxStatusFundsReceived,
		"in_review":             models.TxStatusProcessing,
		"payment_submitted":     models.TxStatusProcessing,
		"returned":              models.TxStatusProcessing,
		"refund_in_flight":      models.TxStatusProcessing,
		"kyc_required":          models.TxStatusProcessing,
		"kyc_in_review":         models.TxStatusProcessing,
		"underfunded":           models.TxStatusProcessing,
		"payment_processed":     models.TxStatusCompleted,
		"refunded":              models.TxStatusRefunded,
		"canceled":              models.TxStatusFailed,
		"error":                 models.TxStatusFailed,
		"undeliverable":         models.TxStatusFailed,
		"refund_failed":         models.TxStatusFailed,
		"missing_return_policy": models.TxStatusFailed,
		"something_new":         models.TxStatusProcessing,
	} {
		require.Equal(t, want, drainStatusFromBridge(state), state)
	}
}

// Only the states of a payout the bank sent back say it is returning.
func TestDrainReturning(t *testing.T) {
	for _, state := range []string{"returned", "refund_in_flight", "refund_failed"} {
		require.True(t, drainReturning(state), state)
	}
	for _, state := range []string{"payment_processed", "payment_submitted", "refunded", "error", "in_review"} {
		require.False(t, drainReturning(state), state)
	}
}

func TestSimulateLiquidationDepositReturnsTheDrain(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/liquidation_addresses/la_1/simulate_deposit": `{"success":true,"drain_id":"drn_sim_1"}`,
	})

	drainID, err := adapter.SimulateLiquidationDeposit(context.Background(), bridgeCustomer, "la_1",
		ramp.SimulatedDeposit{Currency: "USDC", Amount: "25.00", IdempotencyKey: "la-sim:1"})
	require.NoError(t, err)

	require.Equal(t, "drn_sim_1", drainID)
	require.Equal(t, map[string]any{"currency": "usdc", "amount": "25.00"}, (*calls)[0].body)
	require.Equal(t, "la-sim:1", (*calls)[0].header.Get("Idempotency-Key"))
}

// The sandbox refuses a deposit under its own floor (about US$ 1, measured
// 29/10/2026) with a 200 that says so: the provider refusing the amount, as
// Ozav's AMOUNT_BELOW_MINIMUM is. Its floor is not in the answer, so no limit
// rides along.
func TestSimulateLiquidationDepositReadsARefusalAsBelowTheMinimum(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/liquidation_addresses/la_1/simulate_deposit": `{"success":false,` +
			`"message":"Deposit was below minimum amount or failed validation. No drain was created."}`,
	})

	_, err := adapter.SimulateLiquidationDeposit(context.Background(), bridgeCustomer, "la_1",
		ramp.SimulatedDeposit{Currency: "usdc", Amount: "0.80", IdempotencyKey: "la-sim:1"})
	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
}

func TestSimulateLiquidationDepositRefusesAnAnswerWithoutADrain(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/liquidation_addresses/la_1/simulate_deposit": `{"success":true}`,
	})

	_, err := adapter.SimulateLiquidationDeposit(context.Background(), bridgeCustomer, "la_1",
		ramp.SimulatedDeposit{Currency: "usdc", Amount: "25.00", IdempotencyKey: "la-sim:1"})
	require.EqualError(t, err, "bridge: simulated deposit opened no drain")

	// Without a key a retried timeout would simulate a second deposit.
	_, err = adapter.SimulateLiquidationDeposit(context.Background(), bridgeCustomer, "la_1",
		ramp.SimulatedDeposit{Currency: "usdc", Amount: "25.00"})
	require.Error(t, err)
	require.Len(t, *calls, 1)
}
