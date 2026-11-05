package ozav

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// onrampTransferBody is the POST /transfers response measured against the
// sandbox on 2026-08-31 15:18Z, trimmed of the QR data URI's bulk.
const onrampTransferBody = `{
	"id":"ozav_tr_01M1C6CVWNJTXHQ5A490NPVCW0","object":"transfer",
	"customer_id":"ozav_cus_1","quote_id":"ozav_q_1","status":"awaiting_payment",
	"source":{"rail":"pix","type":"fiat_payment","currency":"BRL",
		"payment_instructions":{"rail":"pix",
			"br_code":"00020126570014br.gov.bcb.pix0135sandbox+efc6283f@developers.ozav.io5204000053039865406500.005802BR5912OZAV SANDBOX6009SAO PAULO62150511SIMEFC6283F63044330",
			"intent_id":"sim_pix_efc6283f",
			"expires_at":"2026-08-31T15:48:23Z",
			"qr_code_image":"data:image/svg+xml;utf8,%3Csvg%3E%3C%2Fsvg%3E"}},
	"destination":{"type":"wallet","wallet_id":"ozav_wal_1"},
	"source_leg":{"status":"pending"},"destination_leg":{"status":"pending"},
	"amounts":{"source":{"amount":"500","currency":"BRL"},
		"target":{"amount":"88.2","currency":"USDC"},"rate":"0.1764"},
	"exchange_conditions":{"spread_bps":150},
	"created_at":"2026-08-31T15:18:23Z","updated_at":"2026-08-31T15:18:23Z"
}`

func onrampCreate() ramp.OnrampCreateRequest {
	return ramp.OnrampCreateRequest{
		OnrampQuoteRequest: onrampQuote(),
		ProviderQuoteID:    "ozav_q_1",
		IdempotencyKey:     "onramp-0193f2ac8d5e7b21",
	}
}

// The destination is the customer's Ozav wallet, never the user's own
// address. Measured 2026-08-31: fiat_payment -> crypto_address answers
// 422 UNSUPPORTED_COMBINATION, and the documented flow table lists only
// fiat_payment -> wallet as the on-ramp.
func TestCreateOnrampPaysIntoTheCustomersWalletNotTheUsersAddress(t *testing.T) {
	adapter, fake := newFakeAdapter(t, onrampRoutes())

	_, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.NoError(t, err)

	created := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, created, "the order must be placed on /transfers")
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))

	require.Equal(t, "ozav_cus_1", body["customer_id"])
	require.Equal(t, "ozav_q_1", body["quote_id"], "currencies differ, so the quote is required and authoritative")
	_, hasAmount := body["amount"]
	require.False(t, hasAmount, "exactly one of quote_id or amount — supplying both is a 400")

	source, ok := body["source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fiat_payment", source["type"])
	require.Equal(t, "BRL", source["currency"])
	require.Equal(t, "pix", source["rail"])

	destination, ok := body["destination"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "wallet", destination["type"],
		"fiat_payment -> crypto_address is a measured UNSUPPORTED_COMBINATION")
	require.Equal(t, "ozav_wal_1", destination["wallet_id"])
	require.Equal(t, "polygon", destination["chain"],
		"Ozav needs the delivery network on wallet targets when the quote names a chain")
}

func TestCreateOnrampBrsOnSolanaSendsDestinationChain(t *testing.T) {
	adapter, fake := newFakeAdapter(t, onrampRoutes())

	req := onrampCreate()
	req.Destination = ramp.CryptoLeg{Currency: "BRS", PaymentRail: "solana", Address: "BRS1dPcisLEuuS1bBjbhNzmyE7arjWutD62m2fK19NMg"}

	_, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 2, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	created := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, created)
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))
	destination := body["destination"].(map[string]any)
	require.Equal(t, "solana", destination["chain"])
}

// The BR Code is the whole point of the call: it is what the app shows the
// user to pay. Losing it would leave an order nobody can fund.
func TestCreateOnrampReturnsThePixInstructionsAndTheirExpiry(t *testing.T) {
	adapter, _ := newFakeAdapter(t, onrampRoutes())

	order, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.NoError(t, err)

	require.Equal(t, "ozav_tr_01M1C6CVWNJTXHQ5A490NPVCW0", order.ProviderOrderID)
	require.Equal(t, models.TxStatusAwaitingFunds, order.Status)
	require.Contains(t, order.DepositInstructions["br_code"], "br.gov.bcb.pix")
	require.Equal(t, "sim_pix_efc6283f", order.DepositInstructions["intent_id"])
	require.Contains(t, order.DepositInstructions["qr_code_image"], "data:image/svg+xml")
	require.NotNil(t, order.ExpiresAt, "the BR Code expires; an order that outlives it cannot be paid")
	require.WithinDuration(t, time.Date(2026, 8, 31, 15, 48, 23, 0, time.UTC), *order.ExpiresAt, time.Second)
	require.Equal(t, "500", order.Amounts["source_amount"])
	require.Equal(t, "88.2", order.Amounts["destination_amount"])
}

// The idempotency key is what makes a retry of a money-creating call safe,
// and Ozav requires the header on POST /transfers outright.
func TestCreateOnrampForwardsTheIdempotencyKey(t *testing.T) {
	adapter, fake := newFakeAdapter(t, onrampRoutes())

	_, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.NoError(t, err)

	created := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, created)
	require.Equal(t, "onramp-0193f2ac8d5e7b21", created.header.Get("Idempotency-Key"))
}

// A customer with no wallet cannot receive the crypto leg. Refusing here
// costs one read; letting it through would create a transfer whose
// destination we had to invent.
func TestCreateOnrampRefusesWhenTheCustomerHasNoWallet(t *testing.T) {
	routes := onrampRoutes()
	routes["GET /wallets"] = fakeReply{body: `{"data":[]}`}
	adapter, fake := newFakeAdapter(t, routes)

	_, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.Error(t, err)
	require.Nil(t, fake.call(http.MethodPost, "/transfers"),
		"no transfer may be created for a customer with nowhere to receive")
}

// A self-custody wallet cannot source or receive a managed-custody movement:
// Ozav answers 422 WALLET_NOT_MANAGED. Refusing locally names the real
// condition — this customer's custody model — instead of surfacing a provider
// error about a wallet id the caller never chose.
//
// It is reachable: XIP asks for managed custody per customer, but customers
// created before that fix landed carry a self-custody wallet.
func TestCreateOnrampRefusesASelfCustodyWallet(t *testing.T) {
	routes := onrampRoutes()
	routes["GET /wallets"] = fakeReply{body: `{"data":[{"id":"ozav_wal_1","object":"wallet",
		"customer_id":"ozav_cus_1","custody_model":"self_custody","status":"active"}]}`}
	adapter, fake := newFakeAdapter(t, routes)

	_, err := adapter.CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampCreate())
	require.Error(t, err)
	require.Contains(t, err.Error(), "self_custody",
		"the refusal must name the custody model, which is what an operator has to act on")
	require.Nil(t, fake.call(http.MethodPost, "/transfers"),
		"a transfer Ozav will refuse must not be sent")
}

// The rail always crosses currencies, so a create without the estimate's
// quote id would be Ozav pricing the order at whatever the rate is when it
// lands — not what the user was shown.
func TestCreateOnrampRequiresTheQuote(t *testing.T) {
	req := onrampCreate()
	req.ProviderQuoteID = ""

	_, err := unreachableAdapter(t).CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.ErrorIs(t, err, ramp.ErrEstimateRequired)
}
