package ozav

import (
	"testing"

	"github.com/macrowallets/waas/packages/rampengine/app/models"

	"github.com/stretchr/testify/require"
)

func TestOnrampPayInStatus_AwaitingPayment(t *testing.T) {
	t.Parallel()
	st := onrampPayInStatus(transferResponse{Status: "awaiting_payment"})
	require.Equal(t, models.TxStatusAwaitingFunds, st)
}

func TestOnrampPayInStatus_SourceLegCompleted(t *testing.T) {
	t.Parallel()
	tr := transferResponse{
		Status:      "awaiting_payment",
		SourceLeg:   &transferLeg{Status: "completed"},
		Destination: transferDestination{Type: destinationWallet},
	}
	tr.Amounts.Source = transferAmount{Currency: "BRL"}
	st := onrampPayInStatus(tr)
	require.Equal(t, models.TxStatusFundsReceived, st)
}

func TestOnrampPayInStatus_PayInCompletedIsProcessing(t *testing.T) {
	t.Parallel()
	st := onrampPayInStatus(transferResponse{Status: "completed"})
	require.Equal(t, models.TxStatusProcessing, st)
}

func TestOnrampDeliveryStatus_CompletedWithoutHashStaysProcessing(t *testing.T) {
	t.Parallel()
	st := onrampDeliveryStatus(transferResponse{
		Status:      "completed",
		DestLeg:     &transferLeg{Status: "completed"},
		Destination: transferDestination{Type: "crypto_address"},
	}, "")
	require.Equal(t, models.TxStatusProcessing, st)
}

func TestOnrampDeliveryStatus_CompletedWithHash(t *testing.T) {
	t.Parallel()
	st := onrampDeliveryStatus(transferResponse{Status: "completed"}, "5xSig")
	require.Equal(t, models.TxStatusCompleted, st)
}

func TestOrderFrom_DeliveryFailed(t *testing.T) {
	t.Parallel()
	tr := transferResponse{
		ID:              "tr_delivery",
		Status:          "failed",
		StatusDetail:    statusDetail{Code: "insufficient gas"},
		Destination:     transferDestination{Type: "crypto_address"},
		TransactionHash: "",
	}
	order := orderFrom(tr, nil)
	require.Equal(t, models.TxStatusProcessing, order.Status)
	require.True(t, order.DeliveryFailed)
	require.Equal(t, "insufficient gas", order.FailureReason)
}

// A pay-in's hash is the on-chain credit to the managed wallet, not to the
// user: carrying it would mark the order delivered before the second hop.
func TestOrderFrom_OnlyTheDeliveryCarriesItsHash(t *testing.T) {
	t.Parallel()
	payIn := transferResponse{
		ID: "tr_payin", Status: "completed", TransactionHash: "0xmanaged",
		Destination: transferDestination{Type: destinationWallet},
	}
	payIn.Amounts.Source = transferAmount{Currency: "BRL"}
	require.Empty(t, orderFrom(payIn, nil).TxHash)

	delivery := transferResponse{
		ID: "tr_delivery", Status: "completed", TransactionHash: "0xuser",
		Destination: transferDestination{Type: "crypto_address"},
	}
	order := orderFrom(delivery, nil)
	require.Equal(t, "0xuser", order.TxHash)
	require.Equal(t, models.TxStatusCompleted, order.Status)
}

func TestOnrampPayInStatus_CompletedWaitsForDestinationLeg(t *testing.T) {
	t.Parallel()
	tr := transferResponse{
		Status:      "completed",
		SourceLeg:   &transferLeg{Status: "completed"},
		DestLeg:     &transferLeg{Status: "processing"},
		Destination: transferDestination{Type: destinationWallet},
	}
	tr.Amounts.Source = transferAmount{Currency: "BRL"}
	require.Equal(t, models.TxStatusFundsReceived, onrampPayInStatus(tr))

	tr.DestLeg = &transferLeg{Status: "completed"}
	require.Equal(t, models.TxStatusProcessing, onrampPayInStatus(tr))
}

func TestOnrampDeliveryStatus_CompletedWithBothLegsNeedsNoHash(t *testing.T) {
	t.Parallel()
	st := onrampDeliveryStatus(transferResponse{
		Status:    "completed",
		SourceLeg: &transferLeg{Status: "completed"},
		DestLeg:   &transferLeg{Status: "completed"},
	}, "")
	require.Equal(t, models.TxStatusCompleted, st)
}
