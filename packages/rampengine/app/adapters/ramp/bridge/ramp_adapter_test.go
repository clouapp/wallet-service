package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// exchange is the courtesy rate Bridge quotes for an estimate, shaped as live
// answers it: the sell side below the market, the buy side above.
const exchangeRateBody = `{"midmarket_rate":"0.185000","buy_rate":"0.186000","sell_rate":"0.184000"}`

// call records one request the adapter made, so a multi-step flow can assert
// on the whole sequence rather than only the last leg.
type call struct {
	method string
	path   string
	query  string
	body   map[string]any
	header http.Header
}

// routedAdapter answers each path from a table and records every call in order.
func routedAdapter(t *testing.T, routes map[string]string) (*Adapter, *[]call) {
	t.Helper()
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()}
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		calls = append(calls, c)

		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil), &calls
}

func onrampQuote() ramp.OnrampQuoteRequest {
	return ramp.OnrampQuoteRequest{
		Source:      ramp.FiatLeg{Rail: "pix", Currency: "brl", Amount: "100.00"},
		Destination: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Address: "8nWZB"},
		FeePercent:  "1.50",
	}
}

func offrampQuote() ramp.OfframpQuoteRequest {
	return ramp.OfframpQuoteRequest{
		Source: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Amount: "18.500000", Address: "8nWZB"},
		Destination: ramp.PixDestination{
			Rail: "pix", Currency: "brl", PixKey: "39053344705", PixKeyType: "cpf",
			AccountOwnerName: "Luiza Souza",
		},
		FeePercent: "1.50",
	}
}

// Bridge publishes a pair one way only. When the request runs against that
// direction the adapter fetches the inverse and divides, rather than inverting
// the rate and rounding twice.
func TestEstimateFallsBackToTheInverseDirection(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		// Only brl→usd is published here, the mirror of how the sandbox behaves.
		if r.URL.Query().Get("from") == "brl" && r.URL.Query().Get("to") == "usd" {
			_, _ = w.Write([]byte(`{"midmarket_rate":"0.1835"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)

	estimate, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.Len(t, queries, 2, "the requested direction first, then the published one")
	require.Contains(t, queries[1], "from=brl")

	// The rate shown is what Bridge quoted, not a derived reciprocal.
	require.Equal(t, "0.1835", estimate.Rate)
	// (18.5 - 0.2775) / 0.1835
	require.Equal(t, "99.31", estimate.EstimatedReceive)
}

// Bridge converts at its sell rate, 0.55% under the market, and its receipts
// name the gap "exchange_fee". Quoting the market rate promised every off-ramp
// 0.55% more than it paid: 10 USDC quoted R$52,25 and settled R$51,97 in
// production on 01/10, beside a fee row reading 0%. Live /exchange_rates,
// measured 02/10: mid 5.2108, buy 5.239459, sell 5.182141.
func TestEstimateOfframpPricesAtBridgesSellRateAndNamesTheGap(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"5.2108","buy_rate":"5.239459","sell_rate":"5.182141"}`,
	})
	req := offrampQuote()
	req.Source.Amount = "10.00"
	req.FeePercent = "0.00"

	estimate, err := adapter.EstimateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)

	require.Equal(t, "5.182141", estimate.Rate)
	// 10 * 5.182141
	require.Equal(t, "51.82", estimate.EstimatedReceive)
	// 1 - 5.182141 / 5.2108
	require.Equal(t, "0.55", estimate.ProviderFeePercent)
	// 10 * (5.2108 - 5.182141), in reais
	require.Equal(t, "0.29", estimate.ProviderFeeAmount)
}

// The on-ramp sells reais: brl→usd at its sell rate, under the market too.
func TestEstimateOnrampPricesAtBridgesSellRateAndNamesTheGap(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"0.191909","buy_rate":"0.192964","sell_rate":"0.190854"}`,
	})

	estimate, err := adapter.EstimateOnramp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, onrampQuote())
	require.NoError(t, err)

	require.Equal(t, "0.190854", estimate.Rate)
	// (100.00 - 1.50) * 0.190854
	require.Equal(t, "18.799119", estimate.EstimatedReceive)
	require.Equal(t, "0.55", estimate.ProviderFeePercent)
	// 98.50 * (1 - 0.190854 / 0.191909), in reais
	require.Equal(t, "0.54", estimate.ProviderFeeAmount)
}

// Divided through the published inverse, the side that costs the user is the
// buy rate: more reais per dollar of the inverse is fewer reais paid out.
func TestEstimateThroughTheInverseDividesByBridgesBuyRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("from") == "brl" {
			_, _ = w.Write([]byte(`{"midmarket_rate":"0.191909","buy_rate":"0.192964","sell_rate":"0.190854"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)
	req := offrampQuote()
	req.Source.Amount = "10.00"
	req.FeePercent = "0.00"

	estimate, err := adapter.EstimateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)

	require.Equal(t, "0.192964", estimate.Rate)
	// 10 / 0.192964
	require.Equal(t, "51.82", estimate.EstimatedReceive)
	// 1 - 0.191909 / 0.192964
	require.Equal(t, "0.55", estimate.ProviderFeePercent)
	// 10 / 0.191909 - 10 / 0.192964, in reais
	require.Equal(t, "0.28", estimate.ProviderFeeAmount)
}

// A rate with no sides (the sandbox, the devstub) is the market: there is no
// gap to name, and the estimate says nothing rather than "0%".
func TestEstimateWithoutBridgesSidesNamesNoGap(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"5.45"}`,
	})

	estimate, err := adapter.EstimateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.Equal(t, "5.45", estimate.Rate)
	require.Empty(t, estimate.ProviderFeePercent)
	require.Empty(t, estimate.ProviderFeeAmount)
}

// --- estimates --------------------------------------------------------------

// Bridge lists PIX as a destination rail only, so there is no per-order
// instruction to create — the BRL arrives through a standing virtual account.
// CreateOnramp keeps refusing rather than provisioning one: the method promises
// an order for an amount, and an account that collects any amount at any time
// is not that.
func TestCreateOnrampPointsAtTheVirtualAccountPath(t *testing.T) {
	_, err := unreachableAdapter(t).CreateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OnrampCreateRequest{OnrampQuoteRequest: onrampQuote(), IdempotencyKey: "k"})
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
	require.Contains(t, err.Error(), "virtual account")
}

// On the on-ramp the crypto leg is the destination, not the source — BRL goes
// in and a token comes out — so that is the side refuseCurrency has to read.
// EstimateOnramp was the one money path of four that skipped the guard
// entirely, and the cost was not a wasted round trip: /exchange_rates answers
// `to=sol` with a bare invalid_parameters that carries no sentinel, so a token
// XIP intends to serve one day reached the app as 502 provider_unavailable —
// "the service is down" for something that is simply not offered here.
func TestEstimateOnrampRefusesACurrencyBridgeCannotPrice(t *testing.T) {
	req := onrampQuote()
	req.Destination.Currency = "sol"

	_, err := unreachableAdapter(t).EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// The estimate is arithmetic over a published rate and never places an order,
// so it works on the on-ramp direction as soon as the rail is served: the fee
// comes off the BRL before conversion and `usdc` is quoted as `usd`.
func TestEstimateOnrampAppliesTheRailFeeBeforeConverting(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"0.1835"}`,
	})

	estimate, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, onrampQuote())
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Contains(t, (*calls)[0].query, "from=brl")
	require.Contains(t, (*calls)[0].query, "to=usd")

	require.False(t, estimate.Binding)
	require.Equal(t, "1.50", estimate.FeeAmount)
	require.Equal(t, "brl", estimate.FeeCurrency)
	// (100.00 - 1.50) * 0.1835
	require.Equal(t, "18.074750", estimate.EstimatedReceive)
}

func TestEstimateOfframpAppliesRailFee(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"5.45"}`,
	})

	estimate, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	// usdc is quoted as usd; the usd→brl direction is the published one.
	require.Contains(t, (*calls)[0].query, "from=usd")
	require.Contains(t, (*calls)[0].query, "to=brl")

	require.False(t, estimate.Binding)
	require.Equal(t, "5.45", estimate.Rate)
	require.Equal(t, "0.277500", estimate.FeeAmount)
	require.Equal(t, "usdc", estimate.FeeCurrency)
	// (18.5 - 0.2775) * 5.45
	require.Equal(t, "99.31", estimate.EstimatedReceive)
}

// Bridge refuses a transfer amount past two places — "max precision exceeded
// for this currency", measured in the sandbox on 25/09/2026 — although USDC
// itself carries six and its docs show "1.234567". A whole wallet balance
// reaches the estimate at six, so the quote is for the part Bridge can move,
// and says so: the app sends that amount back on create.
func TestEstimateOfframpQuotesTheAmountBridgeCanMove(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /exchange_rates": `{"midmarket_rate":"5.19"}`,
	})

	req := offrampQuote()
	req.Source.Amount = "3.753027"
	req.FeePercent = "0.00"

	estimate, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.NoError(t, err)

	require.Equal(t, "3.75", estimate.SourceAmount)
	// 3.75 * 5.19, not 3.753027 * 5.19 (19.48): the quote is for what moves.
	require.Equal(t, "19.46", estimate.EstimatedReceive)
}

// A rail whose fee is misconfigured above the order would otherwise reach
// Bridge as a transfer that pays out less than nothing.
func TestEstimateRejectsFeeLargerThanAmount(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /exchange_rates": exchangeRateBody,
	})

	req := offrampQuote()
	req.Source.Amount = "1.00"
	req.FeePercent = "150"

	_, err := adapter.EstimateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.Error(t, err)
}

func TestEstimateRequiresCustomer(t *testing.T) {
	_, err := unreachableAdapter(t).EstimateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7}, offrampQuote())
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// --- off-ramp create --------------------------------------------------------

const offrampTransferBody = `{
	"id":"trf_2","state":"awaiting_funds","amount":"18.500000",
	"source":{"payment_rail":"solana","currency":"usdc","from_address":"8nWZB"},
	"destination":{"payment_rail":"pix","currency":"brl","external_account_id":"ext_1"},
	"source_deposit_instructions":{
		"payment_rail":"solana","currency":"usdc","amount":"18.500000",
		"to_address":"BridgeDepositAddr"
	},
	"receipt":{"developer_fee":"0.277500","exchange_rate":"5.405405","final_amount":"98.50"}
}`

// The PIX key has to exist on the customer before a transfer can name it, so
// the adapter reuses one when it finds it and creates one when it does not.
func TestCreateOfframpRegistersThePixKeyBeforeTransferring(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": `{"id":"ext_1"}`,
		"POST /transfers":                         offrampTransferBody,
	})

	// Uppercase as the client API sends it; Bridge only takes lowercase.
	req := offrampQuote()
	req.Source.Currency = "USDC"
	req.Destination.Currency = "BRL"

	order, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: req, IdempotencyKey: "offramp-key-1"})
	require.NoError(t, err)

	// No listing call: Bridge returns the stored key masked, so a lookup could
	// never match it and would create a duplicate while looking like it
	// deduplicated. The derived idempotency key is what makes the repeat safe.
	require.Len(t, *calls, 2)
	require.Equal(t, http.MethodPost, (*calls)[0].method)
	require.Equal(t, "/customers/cus_1/external_accounts", (*calls)[0].path)
	require.Contains(t, (*calls)[0].header.Get("Idempotency-Key"), "39053344705")
	require.Equal(t, http.MethodPost, (*calls)[1].method)

	destination := (*calls)[1].body["destination"].(map[string]any)
	require.Equal(t, "ext_1", destination["external_account_id"])
	require.Equal(t, "pix", destination["payment_rail"])

	source := (*calls)[1].body["source"].(map[string]any)
	require.Equal(t, "8nWZB", source["from_address"], "the crypto leaves the user's own wallet")
	// Bridge rejects an uppercase code outright — "USDC is not a valid value".
	// The client API speaks in uppercase tokens, so the conversion happens here.
	require.Equal(t, "usdc", source["currency"])
	require.Equal(t, "brl", destination["currency"])

	require.Equal(t, "trf_2", order.ProviderOrderID)
	require.Equal(t, "BridgeDepositAddr", order.DepositInstructions["to_address"])
	require.Equal(t, "usdc", order.FeeCurrency)
}

func TestCreateOfframpSendsTheDocumentedPixAccountShape(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": `{"id":"ext_new"}`,
		"POST /transfers":                         offrampTransferBody,
	})

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: offrampQuote(), IdempotencyKey: "offramp-key-1"})
	require.NoError(t, err)

	require.Len(t, *calls, 2)
	created := (*calls)[0]
	require.Equal(t, "brl", created.body["currency"])
	require.Equal(t, "pix", created.body["account_type"])
	require.Equal(t, "Luiza Souza", created.body["account_owner_name"])
	// `pix_key` is an object whose own `pix_key` field holds the key; a CPF key
	// doubles as the document number.
	pix := created.body["pix_key"].(map[string]any)
	require.Equal(t, "39053344705", pix["pix_key"])
	require.Equal(t, "39053344705", pix["document_number"])

	require.Equal(t, "ext_new", (*calls)[1].body["destination"].(map[string]any)["external_account_id"])
}

func TestExternalAccountIdempotencyKeyDiffersByRecipientDocument(t *testing.T) {
	var idempotencyKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/customers/cus_1/external_accounts" {
			idempotencyKeys = append(idempotencyKeys, r.Header.Get("Idempotency-Key"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ext_phone"}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/transfers" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(offrampTransferBody))
			return
		}
		t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}

	for i, document := range []string{"11111111111", "22222222222"} {
		req := offrampQuote()
		req.Destination.PixKey = "+5511999999999"
		req.Destination.PixKeyType = "phone"
		req.Destination.RecipientDocument = document

		_, err := adapter.CreateOfframp(context.Background(), ref,
			ramp.OfframpCreateRequest{OfframpQuoteRequest: req, IdempotencyKey: "offramp-key-phone-" + string(rune('a'+i))})
		require.NoError(t, err)
	}

	require.Len(t, idempotencyKeys, 2)
	require.NotEqual(t, idempotencyKeys[0], idempotencyKeys[1],
		"same customer and phone PIX key with different recipient documents must not share an idempotency key")
	require.Equal(t, "ext-acct:cus_1:+5511999999999:11111111111", idempotencyKeys[0])
	require.Equal(t, "ext-acct:cus_1:+5511999999999:22222222222", idempotencyKeys[1])
}

func TestCreateOfframpSendsRecipientDocumentForPhonePixKey(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /customers/cus_1/external_accounts": `{"id":"ext_phone"}`,
		"POST /transfers":                         offrampTransferBody,
	})

	req := offrampQuote()
	req.Destination.PixKey = "+5511999999999"
	req.Destination.PixKeyType = "phone"
	req.Destination.RecipientDocument = "39053344705"

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: req, IdempotencyKey: "offramp-key-phone"})
	require.NoError(t, err)

	pix := (*calls)[0].body["pix_key"].(map[string]any)
	require.Equal(t, "+5511999999999", pix["pix_key"])
	require.Equal(t, "39053344705", pix["document_number"])
}

// The service may already know the account, and then no lookup is warranted.
// The key is derived and persisted before the call; without one a retried
// timeout places a second transfer with the user's money.
func TestCreateRequiresIdempotencyKey(t *testing.T) {
	_, err := unreachableAdapter(t).CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: offrampQuote(), ExternalAccountID: "ext_1"})
	require.Error(t, err)
}

func TestCreateOfframpUsesSuppliedExternalAccount(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"POST /transfers": offrampTransferBody,
	})

	_, err := adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{
			OfframpQuoteRequest: offrampQuote(),
			ExternalAccountID:   "ext_supplied",
			IdempotencyKey:      "offramp-key-1",
		})
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Equal(t, "ext_supplied", (*calls)[0].body["destination"].(map[string]any)["external_account_id"])
}

// --- rails ------------------------------------------------------------------

func TestSupportsBothPixRails(t *testing.T) {
	adapter := New(nil, nil)
	require.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayout}))
	require.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin}),
		"the payin is served through a virtual account, not through a transfer")
	require.False(t, adapter.Supports(ramp.Capability{Rail: ramp.Rail("sepa_payin")}))
}

// The token is part of what makes a provider eligible, not something discovered
// after the quote. Bridge prices exactly what /exchange_rates can map, so a
// token outside that list is a quote the user could never redeem.
func TestSupportsRefusesATokenBridgeCannotPrice(t *testing.T) {
	a := New(nil, nil)
	usdc := ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"}
	require.True(t, a.Supports(usdc))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"}))
	require.False(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "SOL", Chain: "solana"}),
		"a token it cannot price is a quote the user could never redeem")
}

// The client writes tokens the way a user reads them and the service passes
// them through untouched, so both cases reach here.
func TestSupportsMatchesTheTokenWhateverCaseItArrivesIn(t *testing.T) {
	a := New(nil, nil)
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "usdc"}))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "UsDt"}))
}

// An unnamed token or chain is a question the caller did not ask. The sticky
// create path resolves on the rail alone, so refusing there would strand every
// order between its estimate and its create.
func TestSupportsAcceptsACapabilityThatNamesNoTokenOrChain(t *testing.T) {
	a := New(nil, nil)
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout}))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"}),
		"the standing account picks the network at delivery, not at request time")
	require.False(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "polygon"}),
		"a chain it does not move is as unredeemable as a token it cannot price")
}

// SOL has no rate and no route at Bridge, so an order in it could never be
// quoted let alone settled. It is refused locally, and as "no provider" rather
// than as a validation error: asking for SOL is legitimate and XIP intends to
// serve it — just not through this provider, and not yet.
func TestSolIsRefusedAsUnservedRatherThanInvalid(t *testing.T) {
	adapter := unreachableAdapter(t)

	req := offrampQuote()
	req.Source.Currency = "SOL"

	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, req)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)

	_, err = adapter.CreateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.OfframpCreateRequest{OfframpQuoteRequest: req, IdempotencyKey: "k"})
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// The stablecoins Bridge does price keep working, whatever case they arrive in.
func TestStablecoinsAreServed(t *testing.T) {
	for _, currency := range []string{"usdc", "USDC", "usdt"} {
		require.NoError(t, refuseCurrency(currency), "currency %q", currency)
	}
	for _, currency := range []string{"sol", "SOL", "btc", ""} {
		require.ErrorIs(t, refuseCurrency(currency), ramp.ErrNoProviderAvailable,
			"currency %q", currency)
	}
}

// A failed Pix key registration left the transfer unsent, so the service may
// release its reservation; a failed POST /transfers may not.
func TestCreateOfframpReportsWhetherTheTransferWasSent(t *testing.T) {
	for failing, notSent := range map[string]bool{
		"POST /customers/cus_1/external_accounts": true,
		"POST /transfers":                         false,
	} {
		t.Run(failing, func(t *testing.T) {
			routes := map[string]string{
				"POST /customers/cus_1/external_accounts": `{"id":"ext_1"}`,
				"POST /transfers":                         offrampTransferBody,
			}
			routes[failing] = `not json`
			adapter, calls := routedAdapter(t, routes)

			_, err := adapter.CreateOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
				ramp.OfframpCreateRequest{OfframpQuoteRequest: offrampQuote(), IdempotencyKey: "offramp-key-1"})
			require.Error(t, err)
			require.Equal(t, notSent, errors.Is(err, ramp.ErrRequestNotSent))
			if notSent {
				require.Len(t, *calls, 1, "no transfer after a failed registration")
			}
		})
	}
}

// A re-read carries Bridge's conversion cut with the settled legs, so an order
// whose webhooks were lost still records what the provider kept.
func TestGetOrderReadsTheExchangeFeeOffTheReceipt(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{
		"GET /transfers/trf_9": `{"id":"trf_9","state":"payment_processed","amount":"10.0",
			"receipt":{"initial_amount":"10.0","exchange_fee":"0.06","final_amount":"51.97","exchange_rate":"5.197000"}}`,
	})

	order, err := adapter.GetOrder(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "trf_9")
	require.NoError(t, err)

	require.Equal(t, "0.06", order.Amounts["exchange_fee"])
	require.Equal(t, "51.97", order.Amounts["destination_amount"])
}

// Bridge withdraws a transfer only while it awaits its deposit, with a DELETE
// whose body says nothing the order needs: the canceled state reaches the row
// through the service and the transfer.updated event.
func TestCancelOfframpDeletesTheTransfer(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"DELETE /transfers/trf_9": `{}`,
	})

	err := adapter.CancelOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "trf_9")
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Equal(t, http.MethodDelete, (*calls)[0].method)
	require.Equal(t, "/transfers/trf_9", (*calls)[0].path)
}

// A transfer that already holds its deposit cannot be withdrawn, and the
// caller must not record it as canceled.
func TestCancelOfframpSurfacesBridgeRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_state","message":"transfer is not awaiting funds"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	adapter := New(func(context.Context) (*Client, error) { return client, nil }, nil)

	err := adapter.CancelOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "trf_9")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func TestCancelOfframpRequiresTheTransferID(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})

	err := adapter.CancelOfframp(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, "")
	require.Error(t, err)
	require.Empty(t, *calls)
}

// The three USD rails are Bridge's too, on the same tokens and chain as Pix.
func TestSupportsTheUSDRails(t *testing.T) {
	adapter := New(nil, nil)
	for _, rail := range ramp.USDRails() {
		require.True(t, adapter.Supports(ramp.Capability{Rail: rail}), rail)
		require.True(t, adapter.Supports(ramp.Capability{Rail: rail, Token: "USDC", Chain: "solana"}), rail)
		require.True(t, adapter.Supports(ramp.Capability{Rail: rail, Token: "USDT", Chain: "solana"}), rail)
		require.False(t, adapter.Supports(ramp.Capability{Rail: rail, Token: "SOL", Chain: "solana"}), rail)
		require.False(t, adapter.Supports(ramp.Capability{Rail: rail, Token: "USDC", Chain: "ethereum"}), rail)
	}
}
