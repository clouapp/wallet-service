package ozav

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// GetOrder is the reconciler's read, so it reports what settled exactly as
// the webhook does (see TestDecodeWebhookCarriesTheSettledTransferFigures).
func TestGetOrderReportsTheSettledFigures(t *testing.T) {
	adapter, fake := newFakeAdapter(t, map[string]fakeReply{
		"GET /transfers/ozav_tr_1": {body: `{"id":"ozav_tr_1","object":"transfer","status":"completed",
			"amounts":{"source":{"amount":"100","currency":"USDC"},
				"target":{"amount":"541.20","currency":"BRL"},"rate":"5.412000"}}`},
		"GET /transfers/ozav_tr_2": {body: `{"id":"ozav_tr_2","object":"transfer","status":"converting",
			"amounts":{"source":{"amount":"500","currency":"BRL"},
				"target":{"amount":"88.2","currency":"USDC"},"rate":"0.1764"}}`},
	})
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	offramp, err := adapter.GetOrder(context.Background(), ref, "ozav_tr_1")
	require.NoError(t, err)
	require.Equal(t, "ozav_tr_1", offramp.ProviderOrderID)
	require.Equal(t, models.TxStatusCompleted, offramp.Status)
	require.Equal(t, map[string]string{
		"source_amount": "100", "destination_amount": "541.20", "exchange_rate": "5.412000",
	}, offramp.Amounts)
	require.NotNil(t, fake.call(http.MethodGet, "/transfers/ozav_tr_1"))

	onramp, err := adapter.GetOrder(context.Background(), ref, "ozav_tr_2")
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"source_amount": "500", "destination_amount": "88.2", "exchange_rate": "5.668934",
	}, onramp.Amounts, "an on-ramp's 0.1764 USDC per BRL is 5.668934 BRL per USDC")
}

// A failed pay-in or payout carries Ozav's reason too, not only a delivery:
// the operator otherwise reads an empty failure on the order.
func TestGetOrderCarriesTheReasonOfAFailedPayinOrPayout(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{
		"GET /transfers/ozav_tr_in": {body: `{"id":"ozav_tr_in","object":"transfer","status":"failed",
			"status_detail":"pix_refunded","source":{"type":"fiat_payment","currency":"BRL"}}`},
		"GET /transfers/ozav_tr_out": {body: `{"id":"ozav_tr_out","object":"transfer","status":"cancelled",
			"status_detail":"pix_key_invalid","destination":{"type":"payout_destination"}}`},
	})
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	payin, err := adapter.GetOrder(context.Background(), ref, "ozav_tr_in")
	require.NoError(t, err)
	require.Equal(t, "pix_refunded", payin.FailureReason)
	require.False(t, payin.DeliveryFailed, "a pay-in is no delivery")

	payout, err := adapter.GetOrder(context.Background(), ref, "ozav_tr_out")
	require.NoError(t, err)
	require.Equal(t, "pix_key_invalid", payout.FailureReason)
	require.False(t, payout.DeliveryFailed)
}

// A create that fails before POST /transfers never reached Ozav with the order,
// so the service may release its reservation; a failed POST /transfers may not.
func TestCreateReportsWhetherTheTransferWasSent(t *testing.T) {
	const internalError = `{"error":{"type":"api_error","code":"INTERNAL_ERROR","message":"x"}}`
	const holderMismatch = `{"error":{"type":"invalid_request_error","code":"PAYOUT_DESTINATION_HOLDER_MISMATCH","message":"x"}}`
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}
	onramp := func(a *Adapter) error {
		_, err := a.CreateOnramp(context.Background(), ref, onrampCreate())
		return err
	}
	offramp := func(a *Adapter) error {
		_, err := a.CreateOfframp(context.Background(), ref, offrampCreate())
		return err
	}
	offrampSettle := func(a *Adapter) error {
		_, err := a.SettleOfframp(context.Background(), ref, ramp.OfframpSettleRequest{
			OfframpCreateRequest: offrampCreate(),
			ExternalAccountID:    "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT",
		})
		return err
	}
	cases := map[string]struct {
		routes  map[string]fakeReply
		failing string
		reply   fakeReply
		create  func(*Adapter) error
		notSent bool
	}{
		"on-ramp wallet lookup fails":    {onrampRoutes(), "GET /wallets", fakeReply{http.StatusInternalServerError, internalError}, onramp, true},
		"on-ramp customer has no wallet": {onrampRoutes(), "GET /wallets", fakeReply{body: `{"data":[]}`}, onramp, true},
		"off-ramp wallet lookup fails":   {offrampRoutes(), "GET /wallets", fakeReply{http.StatusInternalServerError, internalError}, offramp, true},
		"off-ramp destination refused":   {offrampRoutes(), "POST /payout-destinations", fakeReply{http.StatusUnprocessableEntity, holderMismatch}, offramp, true},
		"on-ramp transfer itself fails":  {onrampRoutes(), "POST /transfers", fakeReply{http.StatusInternalServerError, internalError}, onramp, false},
		"off-ramp transfer itself fails": {offrampRoutes(), "POST /transfers", fakeReply{http.StatusInternalServerError, internalError}, offrampSettle, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.routes[c.failing] = c.reply
			adapter, fake := newFakeAdapter(t, c.routes)

			err := c.create(adapter)
			require.Error(t, err)
			require.Equal(t, c.notSent, errors.Is(err, ramp.ErrRequestNotSent))
			if c.notSent {
				require.Nil(t, fake.call(http.MethodPost, "/transfers"))
			}
		})
	}
}

// A retry under a key Ozav still holds claimed answers 202 "processing" with no
// transfer: the outcome is unknown, so it is neither an order nor unsent.
func TestATransferStillProcessingIsNoOrder(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}
	processing := fakeReply{http.StatusAccepted, `{"status":"processing"}`}
	cases := map[string]struct {
		routes map[string]fakeReply
		create func(*Adapter) (*ramp.RampOrder, error)
	}{
		"on-ramp": {onrampRoutes(), func(a *Adapter) (*ramp.RampOrder, error) {
			return a.CreateOnramp(context.Background(), ref, onrampCreate())
		}},
		"off-ramp": {offrampRoutes(), func(a *Adapter) (*ramp.RampOrder, error) {
			return a.SettleOfframp(context.Background(), ref, ramp.OfframpSettleRequest{
				OfframpCreateRequest: offrampCreate(),
				ExternalAccountID:    "ozav_pd_01M1C65Z9C4K4Y8MJXGHEQ4QAT",
			})
		}},
		"delivery": {deliveryRoutes(), func(a *Adapter) (*ramp.RampOrder, error) {
			return a.DeliverOnramp(context.Background(), ref, deliveryRequest())
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.routes["POST /transfers"] = processing
			adapter, _ := newFakeAdapter(t, c.routes)

			order, err := c.create(adapter)
			require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
			require.NotErrorIs(t, err, ramp.ErrRequestNotSent)
			require.Nil(t, order)
		})
	}
}

// Live transfers carry status_detail as {code, message, retryable}; the reason
// on the order is the code and Ozav's provider-neutral message.
func TestGetOrderReadsAStatusDetailObject(t *testing.T) {
	adapter, _ := newFakeAdapter(t, map[string]fakeReply{
		"GET /transfers/ozav_tr_out": {body: `{"id":"ozav_tr_out","object":"transfer","status":"failed",
			"status_detail":{"code":"AMOUNT_BELOW_MINIMUM","message":"Minimum amount for this off-ramp is 185 USDC.","retryable":false},
			"destination":{"type":"payout_destination"}}`},
	})
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}

	payout, err := adapter.GetOrder(context.Background(), ref, "ozav_tr_out")

	require.NoError(t, err)
	require.Equal(t, models.TxStatusFailed, payout.Status)
	require.Equal(t, "AMOUNT_BELOW_MINIMUM: Minimum amount for this off-ramp is 185 USDC.", payout.FailureReason)
}
