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

// deliveryCustomerBody is GET /customers/{id} as the sandbox answers it, with
// the fields the Travel Rule needs. Measured 31/08.
const deliveryCustomerBody = `{
	"id":"ozav_cus_1","object":"customer","type":"individual","status":"approved",
	"email":"luiza@example.com",
	"individual":{
		"legal_name":"Luiza Souza",
		"date_of_birth":"1990-04-12",
		"tax_id":{"type":"cpf","value":"39053344705","country":"BR"},
		"residential_address":{"line1":"Avenida Paulista","number":"1000",
			"neighborhood":"Bela Vista","city":"Sao Paulo","state":"SP",
			"postal_code":"01310000","country":"BR"}
	},
	"verification":{"mode":"full","status":"approved"},
	"requirements":{"due":[],"missing_fields":[]}
}`

const deliveryTransferBody = `{
	"id":"ozav_tr_delivery_1","object":"transfer","customer_id":"ozav_cus_1",
	"status":"completed",
	"source":{"type":"wallet","wallet_id":"ozav_wal_1","currency":"USDC"},
	"destination":{"type":"crypto_address","address":"0x742d","network":"polygon","currency":"USDC"},
	"amounts":{"source":{"amount":"88.2","currency":"USDC"},
		"target":{"amount":"88.2","currency":"USDC"}},
	"created_at":"2026-08-31T16:00:00Z"
}`

func deliveryRequest() ramp.OnrampDeliveryRequest {
	return ramp.OnrampDeliveryRequest{
		Amount:              "88.200000",
		Destination:         ramp.CryptoLeg{Currency: "usdc", PaymentRail: "polygon", Address: "0x742d35Cc"},
		AttestedOwnershipAt: time.Date(2026, 8, 31, 15, 18, 23, 0, time.UTC),
		IdempotencyKey:      "onramp-deliver-0193f2ac8d5e",
	}
}

// The delivery is a same-asset move, so it carries an amount and no quote —
// Ozav takes exactly one of the two and a request with both is a 400.
func TestDeliverOnrampSendsTheAmountAndNoQuote(t *testing.T) {
	adapter, fake := newFakeAdapter(t, deliveryRoutes())

	_, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, deliveryRequest())
	require.NoError(t, err)

	sent := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, sent)
	var body map[string]any
	require.NoError(t, json.Unmarshal(sent.body, &body))

	_, hasQuote := body["quote_id"]
	require.False(t, hasQuote, "a same-asset move is not an exchange — exactly one of quote_id or amount")
	amount, ok := body["amount"].(map[string]any)
	require.True(t, ok, "amount is an object, not a string — a string is a measured 400")
	require.Equal(t, "88.200000", amount["amount"])
	require.Equal(t, "USDC", amount["currency"])

	source := body["source"].(map[string]any)
	require.Equal(t, "wallet", source["type"])
	require.Equal(t, "ozav_wal_1", source["wallet_id"])

	destination := body["destination"].(map[string]any)
	require.Equal(t, "crypto_address", destination["type"])
	require.Equal(t, "0x742d35Cc", destination["address"], "the user's own wallet")
	require.Equal(t, "polygon", destination["network"])
	require.Equal(t, "USDC", destination["currency"])
	require.Equal(t, "onramp-deliver-0193f2ac8d5e", sent.header.Get("Idempotency-Key"))
}

// The destination network is the same vocabulary as the source chain: a rail
// stored as "Polygon" must not reach Ozav as a network it does not know.
func TestDeliverOnrampNormalizesTheDestinationNetwork(t *testing.T) {
	adapter, fake := newFakeAdapter(t, deliveryRoutes())
	req := deliveryRequest()
	req.Destination.PaymentRail = " Polygon "

	_, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	sent := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, sent)
	var body map[string]any
	require.NoError(t, json.Unmarshal(sent.body, &body))
	require.Equal(t, "polygon", body["destination"].(map[string]any)["network"])
	require.Equal(t, "polygon", body["source"].(map[string]any)["chain"])
}

// BCB Resolution 520 requires the originator's identity on a send that leaves
// custody. It is read back from the customer Ozav already holds rather than
// plumbed down from the service: the same data, one source, and no PII
// crossing a layer that has no other use for it.
func TestDeliverOnrampBuildsTheTravelRuleFromTheRegisteredCustomer(t *testing.T) {
	adapter, fake := newFakeAdapter(t, deliveryRoutes())

	_, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, deliveryRequest())
	require.NoError(t, err)

	require.NotNil(t, fake.call(http.MethodGet, "/customers/ozav_cus_1"),
		"the originator comes from the customer we registered")

	sent := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, sent)
	var body map[string]any
	require.NoError(t, json.Unmarshal(sent.body, &body))

	rule, ok := body["travel_rule"].(map[string]any)
	require.True(t, ok, "a crypto_address destination without travel_rule is a measured 422")

	originator := rule["originator"].(map[string]any)
	require.Equal(t, "Luiza Souza", originator["name"])
	documents := originator["identity_documents"].([]any)
	require.Len(t, documents, 1)
	document := documents[0].(map[string]any)
	require.Equal(t, "cpf", document["type"], "at least one entry must be cpf or cnpj")
	require.Equal(t, "39053344705", document["number"])
	require.Equal(t, "BR", document["issuing_country"], "required on every entry")

	address := originator["address"].(map[string]any)
	require.Equal(t, "Avenida Paulista", address["street_line_1"])
	require.Equal(t, "Sao Paulo", address["city"])
	require.Equal(t, "BR", address["country"])

	beneficiary := rule["beneficiary"].(map[string]any)
	require.Equal(t, "Luiza Souza", beneficiary["name"])
	require.Equal(t, "self_custodied", beneficiary["wallet_type"], "hosted is rejected on this path")
	require.Equal(t, true, beneficiary["is_self"], "the destination belongs to our own customer")
	require.Equal(t, "0x742d35Cc", beneficiary["account"])
	require.Equal(t, "2026-08-31T15:18:23Z", beneficiary["wallet_attested_ownership_at"])
}

// The amount arrives from a numeric(38,18) column, which Postgres renders with
// all eighteen decimals. Ozav's DecimalAmount accepts at most six and answers
// 400 INVALID_REQUEST param=amount.amount otherwise — measured against the
// sandbox on 31/08, from the live smoke, with "88.200000000000000000".
//
// Trimming belongs here rather than in the service for the same reason
// normalizeIdempotencyKey does: six digits is Ozav's accounting precision, not
// a fact about what XIP owes the user.
func TestDeliverOnrampSendsTheAmountAtOzavsPrecision(t *testing.T) {
	adapter, fake := newFakeAdapter(t, deliveryRoutes())

	req := deliveryRequest()
	req.Amount = "88.200000000000000000"

	_, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	sent := fake.call(http.MethodPost, "/transfers")
	require.NotNil(t, sent)
	var body map[string]any
	require.NoError(t, json.Unmarshal(sent.body, &body))
	amount := body["amount"].(map[string]any)
	require.Equal(t, "88.200000", amount["amount"],
		"eighteen decimals is a measured 400 — Ozav takes at most six")
}

func TestDeliverOnrampReportsTheDeliveryOrder(t *testing.T) {
	adapter, _ := newFakeAdapter(t, deliveryRoutes())

	order, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, deliveryRequest())
	require.NoError(t, err)

	require.Equal(t, "ozav_tr_delivery_1", order.ProviderOrderID)
	require.Equal(t, models.TxStatusProcessing, order.Status,
		"delivery completed upstream without a tx hash is not credited yet")
}

// A customer whose registered identity is missing the CPF cannot satisfy
// Resolution 520, and sending anyway buys a 422 in exchange for a round trip.
// Refusing locally also names the real problem: the KYC record is incomplete.
func TestDeliverOnrampRefusesWithoutTheOriginatorDocument(t *testing.T) {
	routes := deliveryRoutes()
	routes["GET /customers/ozav_cus_1"] = fakeReply{body: `{
		"id":"ozav_cus_1","type":"individual","status":"approved",
		"individual":{"legal_name":"Luiza Souza",
			"residential_address":{"line1":"Avenida Paulista","city":"Sao Paulo","country":"BR"}}
	}`}
	adapter, fake := newFakeAdapter(t, routes)

	_, err := adapter.DeliverOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, deliveryRequest())
	require.Error(t, err)
	require.Nil(t, fake.call(http.MethodPost, "/transfers"),
		"no send may go out that Resolution 520 will refuse")
}
