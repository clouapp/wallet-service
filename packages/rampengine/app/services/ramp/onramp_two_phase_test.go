package ramp_test

import (
	"testing"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"

	"github.com/stretchr/testify/require"
)

func TestPayInReadyForDelivery_OzavProcessing(t *testing.T) {
	t.Parallel()
	tx := &models.Transaction{
		Type: models.TransactionTypeOnramp, Provider: models.ProviderOzav,
		Status: models.TxStatusProcessing, GatewayOrderID: "ozav_tr_1",
	}
	require.True(t, rampsvc.PayInReadyForDelivery(tx))
}

func TestPayInReadyForDelivery_NotBeforePix(t *testing.T) {
	t.Parallel()
	tx := &models.Transaction{
		Type: models.TransactionTypeOnramp, Provider: models.ProviderOzav,
		Status: models.TxStatusAwaitingFunds, GatewayOrderID: "ozav_tr_1",
	}
	require.False(t, rampsvc.PayInReadyForDelivery(tx))
}

func TestNeedsProviderRefreshUntilCredited(t *testing.T) {
	t.Parallel()
	sig := "sig"
	tx := &models.Transaction{
		Type: models.TransactionTypeOnramp, Provider: models.ProviderOzav,
		Status: models.TxStatusProcessing, GatewayOrderID: "ozav_tr_1",
	}
	require.True(t, rampsvc.NeedsProviderRefresh(tx))
	tx.DepositTxSignature = support.Ptr(sig)
	require.False(t, rampsvc.NeedsProviderRefresh(tx))
}

func TestOnrampCredited_ClosedDeliveryWithoutHash(t *testing.T) {
	id := "ozav_tr_delivery"
	tx := &models.Transaction{Status: models.TxStatusCompleted, DeliveryOrderID: &id}
	require.True(t, rampsvc.OnrampCredited(tx))
	require.False(t, rampsvc.NeedsProviderRefresh(tx))

	tx.Status = models.TxStatusProcessing
	require.False(t, rampsvc.OnrampCredited(tx))
}
