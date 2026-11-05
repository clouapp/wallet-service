package blindpay_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const (
	instancePrefix = "/v1/instances/in_000000000001/"
	customerID     = "re_000000000001"
	walletID       = "bw_000000000001"
	partnerFee     = "pf_000000000001"
	quoteID        = "qu_000000000001"
)

type stubAnswer struct {
	status int
	body   string
}

// payinServer answers each "METHOD relative/path" from routes (404 for any
// other) and records every call in order.
func payinServer(t *testing.T, mode string, routes map[string]stubAnswer) (*blindpay.Adapter, *blindpay.Client, func() []feeCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []feeCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, feeCall{r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		answer, ok := routes[r.Method+" "+strings.TrimPrefix(r.URL.EscapedPath(), instancePrefix)]
		if !ok {
			answer = stubAnswer{http.StatusNotFound, `{"code":"NOT_ROUTED"}`}
		}
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{
		APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: mode, BaseURL: server.URL + "/v1",
	})
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)
	return adapter, client, func() []feeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]feeCall(nil), calls...)
	}
}

// quoteBody is a valid CreatePayinQuoteOut for 100.55 BRL, with overrides;
// a nil override removes the field.
func quoteBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"id":                   quoteID,
		"expires_at":           time.Now().Add(5 * time.Minute).Unix(),
		"commercial_quotation": 495,
		"blindpay_quotation":   505,
		"receiver_amount":      1960,
		"sender_amount":        10055,
		"partner_fee_amount":   31,
		"flat_fee":             50,
		"billing_fee_amount":   0,
		"is_otc":               false,
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func walletList(network string) string {
	return `[{"id":"` + walletID + `","network":"` + network + `","address":"` + walletAddr + `"}]`
}

func happyRoutes(t *testing.T, network string, quote string) map[string]stubAnswer {
	return map[string]stubAnswer{
		"GET customers/" + customerID + "/blockchain-wallets": {200, walletList(network)},
		"GET partner-fees":  {200, "[" + feeProfile(partnerFee, 155, 0) + "]"},
		"POST payin-quotes": {200, quote},
	}
}

func onrampRequest(amount, feePercent string) ramp.OnrampQuoteRequest {
	return ramp.OnrampQuoteRequest{
		Source:      ramp.FiatLeg{Rail: "pix", Currency: "BRL", Amount: amount},
		Destination: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Address: walletAddr},
		FeePercent:  feePercent,
	}
}

var payinRef = ramp.CustomerRef{ExternalID: customerID}

func TestEstimateOnramp_HappyPathCallsInOrderWithTheExactBody(t *testing.T) {
	quote := quoteBody(t, nil)
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quote))

	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 3)
	assert.Equal(t, "GET "+instancePrefix+"customers/"+customerID+"/blockchain-wallets", got[0].method+" "+got[0].path)
	assert.Equal(t, "GET "+instancePrefix+"partner-fees", got[1].method+" "+got[1].path)
	assert.Equal(t, "POST "+instancePrefix+"payin-quotes", got[2].method+" "+got[2].path)
	assert.JSONEq(t, `{
		"blockchain_wallet_id": "`+walletID+`",
		"currency_type": "sender",
		"cover_fees": false,
		"request_amount": 10055,
		"payment_method": "pix",
		"token": "USDB",
		"partner_fee_id": "`+partnerFee+`"
	}`, string(got[2].body))

	assert.Equal(t, quoteID, est.ProviderQuoteID)
	assert.True(t, est.Binding)
	require.NotNil(t, est.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), *est.ExpiresAt, 5*time.Second)
	assert.Equal(t, "5.050000", est.Rate)
	assert.Equal(t, "19.60", est.EstimatedReceive)
	assert.Equal(t, "0.31", est.FeeAmount)
	assert.Equal(t, "usdc", est.FeeCurrency)
	assert.Equal(t, "1.55", est.FeePercent)
	assert.Equal(t, "2.52", est.ProviderFeeAmount, "0.50 token at 5.05, rounded down")
	assert.Equal(t, "100.55", est.SourceAmount)
	assert.JSONEq(t, quote, string(est.Raw))
}

func TestEstimateOnramp_WithoutFeeSendsNoPartnerFee(t *testing.T) {
	routes := happyRoutes(t, "solana_devnet", quoteBody(t, map[string]any{"partner_fee_amount": 0}))
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, routes)

	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", ""))
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 2, "no partner fee listing without a fee")
	assert.Equal(t, http.MethodGet, got[0].method)
	assert.Equal(t, http.MethodPost, got[1].method)
	var body map[string]any
	require.NoError(t, json.Unmarshal(got[1].body, &body))
	assert.NotContains(t, body, "partner_fee_id")
	assert.Equal(t, "0.00", est.FeeAmount)
}

func TestEstimateOnramp_FractionalCentsAreRoundedDownAndReported(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quoteBody(t, nil)))

	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.555", "1.55"))
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 3)
	var body map[string]any
	require.NoError(t, json.Unmarshal(got[2].body, &body))
	assert.Equal(t, float64(10055), body["request_amount"])
	assert.Equal(t, "100.55", est.SourceAmount)
}

func TestEstimateOnramp_WholeAmountIsQuotedInCents(t *testing.T) {
	routes := happyRoutes(t, "solana_devnet", quoteBody(t, map[string]any{"sender_amount": 10000}))
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, routes)

	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100", "1.55"))
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(calls()[2].body, &body))
	assert.Equal(t, float64(10000), body["request_amount"])
	assert.Equal(t, "100.00", est.SourceAmount)
}

func TestEstimateOnramp_RefusesLocallyWithoutCalling(t *testing.T) {
	cases := map[string]struct {
		ref  ramp.CustomerRef
		req  ramp.OnrampQuoteRequest
		want error
	}{
		"no customer":      {ramp.CustomerRef{}, onrampRequest("100", ""), ramp.ErrIntegrationRequired},
		"not brl":          {payinRef, func() ramp.OnrampQuoteRequest { r := onrampRequest("100", ""); r.Source.Currency = "USD"; return r }(), ramp.ErrRequestNotSent},
		"below minimum":    {payinRef, onrampRequest("4.99", ""), ramp.ErrAmountBelowMinimum},
		"rounds below min": {payinRef, onrampRequest("4.999", ""), ramp.ErrAmountBelowMinimum},
		"zero":             {payinRef, onrampRequest("0", ""), ramp.ErrAmountBelowMinimum},
		"not a number":     {payinRef, onrampRequest("abc", ""), ramp.ErrRequestNotSent},
		"negative":         {payinRef, onrampRequest("-100", ""), ramp.ErrRequestNotSent},
		"exponent":         {payinRef, onrampRequest("1e3", ""), ramp.ErrRequestNotSent},
		"bad fee":          {payinRef, onrampRequest("100", "abc"), ramp.ErrRequestNotSent},
		"evm address": {payinRef, func() ramp.OnrampQuoteRequest {
			r := onrampRequest("100", "")
			r.Destination.Address = "0x1234567890abcdef1234567890abcdef12345678"
			return r
		}(), ramp.ErrInvalidWallet},
		"short base58": {payinRef, func() ramp.OnrampQuoteRequest {
			r := onrampRequest("100", "")
			r.Destination.Address = "1111111111111111111111111111111"
			return r
		}(), ramp.ErrInvalidWallet},
		"other chain": {payinRef, func() ramp.OnrampQuoteRequest {
			r := onrampRequest("100", "")
			r.Destination.PaymentRail = "ethereum"
			return r
		}(), ramp.ErrNoProviderAvailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, calls := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quoteBody(t, nil)))
			_, err := adapter.EstimateOnramp(context.Background(), tc.ref, tc.req)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, calls())
		})
	}
}

func TestEstimateOnramp_BelowMinimumNamesTheBound(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, nil)
	_, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("4.99", ""))
	var limit *ramp.LimitViolation
	require.True(t, errors.As(err, &limit))
	assert.Equal(t, "5.00", limit.LimitBRL)
}

func TestEstimateOnramp_ModeDecidesNetworkAndToken(t *testing.T) {
	cases := map[string]struct{ network, token string }{
		models.ProviderModeSandbox:    {"solana_devnet", "USDB"},
		models.ProviderModeProduction: {"solana", "USDC"},
	}
	for mode, want := range cases {
		t.Run(mode, func(t *testing.T) {
			// The listing only holds the wallet on the other network, so the
			// adapter must register it on want.network.
			other := "solana"
			if want.network == "solana" {
				other = "solana_devnet"
			}
			routes := happyRoutes(t, want.network, quoteBody(t, nil))
			routes["GET customers/"+customerID+"/blockchain-wallets"] = stubAnswer{200, walletList(other)}
			routes["POST customers/"+customerID+"/blockchain-wallets"] = stubAnswer{200, `{"id":"` + walletID + `"}`}
			adapter, _, calls := payinServer(t, mode, routes)

			_, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
			require.NoError(t, err)

			got := calls()
			require.Len(t, got, 4)
			var wallet, quote map[string]any
			require.NoError(t, json.Unmarshal(got[1].body, &wallet))
			assert.Equal(t, want.network, wallet["network"])
			require.NoError(t, json.Unmarshal(got[3].body, &quote))
			assert.Equal(t, want.token, quote["token"])
		})
	}
}

func TestEstimateOnramp_RefusesAnInvalidQuote(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	cases := map[string]map[string]any{
		"short id":                {"id": "qu_1"},
		"no id":                   {"id": nil},
		"expired seconds":         {"expires_at": past.Unix()},
		"expired millis":          {"expires_at": past.UnixMilli()},
		"sender differs":          {"sender_amount": 10054},
		"no sender":               {"sender_amount": nil},
		"zero receiver":           {"receiver_amount": 0},
		"no receiver":             {"receiver_amount": nil},
		"zero quotation":          {"blindpay_quotation": 0},
		"negative quotation":      {"blindpay_quotation": -505},
		"no quotation":            {"blindpay_quotation": nil},
		"negative partner fee":    {"partner_fee_amount": -1},
		"negative flat fee":       {"flat_fee": -1},
		"receiver not a number":   {"receiver_amount": "1960"},
		"expires_at not a number": {"expires_at": "soon"},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quoteBody(t, override)))
			est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
			assert.Nil(t, est)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.NotContains(t, err.Error(), "100")
			assert.NotContains(t, err.Error(), "1960")
		})
	}
}

func TestEstimateOnramp_ExpiryInMillisAndAbsent(t *testing.T) {
	in := time.Now().Add(3 * time.Minute)
	cases := map[string]struct {
		value any
		want  time.Time
	}{
		"millis": {in.UnixMilli(), in},
		"absent": {nil, time.Now().Add(5 * time.Minute)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			override := map[string]any{"expires_at": tc.value}
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quoteBody(t, override)))
			est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
			require.NoError(t, err)
			assert.WithinDuration(t, tc.want, *est.ExpiresAt, 5*time.Second)
		})
	}
	t.Run("null", func(t *testing.T) {
		quote := strings.Replace(quoteBody(t, map[string]any{"expires_at": 1}), `"expires_at":1`, `"expires_at":null`, 1)
		adapter, _, _ := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quote))
		est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), *est.ExpiresAt, 5*time.Second)
	})
}

func TestEstimateOnramp_UnknownPartnerFeeIsForgotten(t *testing.T) {
	for name, rejection := range map[string]stubAnswer{
		"fees not found": {http.StatusNotFound, `{"code":"FEES_NOT_FOUND","message":"partner_fee_not_found"}`},
		"bare 404":       {http.StatusNotFound, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			routes := happyRoutes(t, "solana_devnet", "")
			routes["POST payin-quotes"] = rejection
			adapter, _, calls := payinServer(t, models.ProviderModeSandbox, routes)

			_, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			_, err = adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
			require.Error(t, err)

			// The wallet stays cached; the partner fee is listed again.
			var fees int
			for _, c := range calls() {
				if strings.HasSuffix(c.path, "/partner-fees") {
					fees++
				}
			}
			assert.Equal(t, 2, fees)
		})
	}
}

func TestEstimateOnramp_OtherRejectionsKeepThePartnerFee(t *testing.T) {
	routes := happyRoutes(t, "solana_devnet", "")
	routes["POST payin-quotes"] = stubAnswer{http.StatusBadRequest, `{"code":"CUSTOMERS_KYC_NOT_APPROVED"}`}
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, routes)

	for range 2 {
		_, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
		require.ErrorIs(t, err, ramp.ErrKycPending)
	}
	var fees int
	for _, c := range calls() {
		if strings.HasSuffix(c.path, "/partner-fees") {
			fees++
		}
	}
	assert.Equal(t, 1, fees)
}

func TestEstimateOnramp_RateKeepsAFractionalQuotation(t *testing.T) {
	quote := quoteBody(t, map[string]any{"blindpay_quotation": 505.4321, "flat_fee": 50})
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quote))
	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
	require.NoError(t, err)
	assert.Equal(t, "5.054321", est.Rate)
	// 50 × 505.4321 / 10^4 = 2.5271605 → 2.52
	assert.Equal(t, "2.52", est.ProviderFeeAmount)
}

func TestEstimateOnramp_ProviderFeeIsEmptyWithoutAFlatFee(t *testing.T) {
	quote := quoteBody(t, map[string]any{"flat_fee": nil})
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, happyRoutes(t, "solana_devnet", quote))
	est, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
	require.NoError(t, err)
	assert.Empty(t, est.ProviderFeeAmount)
}

func TestEstimateOnramp_GoneWalletIsForgotten(t *testing.T) {
	routes := happyRoutes(t, "solana_devnet", "")
	routes["POST payin-quotes"] = stubAnswer{http.StatusNotFound, `{"code":"WALLETS_NOT_FOUND","message":"wallet_not_found"}`}
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, routes)

	for range 2 {
		_, err := adapter.EstimateOnramp(context.Background(), payinRef, onrampRequest("100.55", "1.55"))
		require.ErrorIs(t, err, ramp.ErrProviderFailed)
	}
	var wallets, fees int
	for _, c := range calls() {
		switch {
		case strings.HasSuffix(c.path, "/blockchain-wallets"):
			wallets++
		case strings.HasSuffix(c.path, "/partner-fees"):
			fees++
		}
	}
	assert.Equal(t, 2, wallets, "the wallet is listed again")
	assert.Equal(t, 1, fees, "the partner fee stays cached")
}

const payinID = "pi_000000000001"

// payinBody is a valid PayinOut (and CreatePayinOut) for the 100.55 BRL quote,
// with overrides; a nil override removes the field.
func payinBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"id":                   payinID,
		"status":               "processing",
		"customer_id":          customerID,
		"payin_quote_id":       quoteID,
		"pix_code":             "00020101021226790014br.gov.bcb.pix2557brcode",
		"sender_amount":        10055,
		"receiver_amount":      1960,
		"token":                "USDB",
		"network":              "solana_devnet",
		"tracking_transaction": map[string]any{"step": "processing"},
		"tracking_payment":     map[string]any{"step": "processing"},
		"tracking_complete":    map[string]any{"step": "processing"},
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func onrampCreate() ramp.OnrampCreateRequest {
	return ramp.OnrampCreateRequest{
		OnrampQuoteRequest: onrampRequest("100.55", "1.55"),
		ProviderQuoteID:    quoteID,
		IdempotencyKey:     "onramp-0193f2ac8d5e7b21",
	}
}

// captureLog swaps the log facade for a mock for one test and restores the
// package's test logger after it. Upstream-call telemetry is allowed; every
// other call must be expected by the test.
func captureLog(t *testing.T) *mockslog.Log {
	t.Helper()
	logger := &mockslog.Log{}
	mock.Factory().App().On("MakeLog").Return(logger)
	logger.On("With", testifymock.Anything).Return(logger).Maybe()
	logger.On("Info", testifymock.Anything).Return().Maybe()
	logger.On("Warning", testifymock.Anything).Return().Maybe()
	t.Cleanup(func() { mock.Factory().Log() })
	return logger
}

func TestCreateOnramp_HappyPathRedeemsTheQuoteUnderTheTransactionsKey(t *testing.T) {
	created := payinBody(t, nil)
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, created},
	})

	order, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "POST "+instancePrefix+"payins/evm", got[0].method+" "+got[0].path)
	assert.Equal(t, "onramp-0193f2ac8d5e7b21", got[0].idempotency)
	assert.JSONEq(t, `{"payin_quote_id":"`+quoteID+`"}`, string(got[0].body))

	assert.Equal(t, payinID, order.ProviderOrderID)
	assert.Equal(t, models.TxStatusAwaitingFunds, order.Status)
	assert.Equal(t, map[string]any{"br_code": "00020101021226790014br.gov.bcb.pix2557brcode"}, order.DepositInstructions)
	require.NotNil(t, order.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(30*time.Minute), *order.ExpiresAt, 5*time.Second)
	assert.Equal(t, map[string]string{"source_amount": "100.55", "destination_amount": "19.60"}, order.Amounts)
	assert.Equal(t, "1.55", order.FeePercent)
	assert.Empty(t, order.TxHash)
	assert.JSONEq(t, created, string(order.Raw))
}

func TestCreateOnramp_MissingAmountsAreLeftOut(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, payinBody(t, map[string]any{"sender_amount": nil, "receiver_amount": nil})},
	})
	order, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.NoError(t, err)
	assert.Empty(t, order.Amounts)
}

// The sandbox answers a placeholder instead of a real BR Code; it is still a
// code, so the order is created.
func TestCreateOnramp_SandboxPlaceholderCodePasses(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, payinBody(t, map[string]any{"pix_code": "<development>"})},
	})
	order, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.NoError(t, err)
	assert.Equal(t, "<development>", order.DepositInstructions["br_code"])
}

// Without a code the user has nothing to pay.
func TestCreateOnramp_RefusesAPayinItCannotPay(t *testing.T) {
	cases := map[string]string{
		"no pix code":    payinBody(t, map[string]any{"pix_code": nil}),
		"empty pix code": payinBody(t, map[string]any{"pix_code": "  "}),
		"null pix code":  strings.Replace(payinBody(t, map[string]any{"pix_code": "x"}), `"pix_code":"x"`, `"pix_code":null`, 1),
		"short id":       payinBody(t, map[string]any{"id": "pi_1"}),
		"no id":          payinBody(t, map[string]any{"id": nil}),
		"not json":       `{"id":`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.On("Errorf", testifymock.Anything, testifymock.Anything).Return().Maybe()
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
				"POST payins/evm": {200, body},
			})
			order, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
		})
	}
}

func TestCreateOnramp_RefusesLocallyWithoutCalling(t *testing.T) {
	noQuote := onrampCreate()
	noQuote.ProviderQuoteID = ""
	noKey := onrampCreate()
	noKey.IdempotencyKey = ""
	cases := map[string]struct {
		ref  ramp.CustomerRef
		req  ramp.OnrampCreateRequest
		want error
	}{
		"no customer": {ramp.CustomerRef{}, onrampCreate(), ramp.ErrIntegrationRequired},
		"no quote":    {payinRef, noQuote, ramp.ErrEstimateRequired},
		"no key":      {payinRef, noKey, ramp.ErrRequestNotSent},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
			_, err := adapter.CreateOnramp(context.Background(), tc.ref, tc.req)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, calls())
		})
	}
}

// A create that got no answer may still have created the payin: the outcome
// is unknown, and a retry under the same key resolves it.
func TestCreateOnramp_TimeoutIsAnUnknownOutcome(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	client := blindpay.NewClient(blindpay.Config{APIKey: "bp_test_key", InstanceID: "in_000000000001",
		Mode: models.ProviderModeSandbox, BaseURL: server.URL + "/v1", Timeout: 50 * time.Millisecond})
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)

	_, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.ErrorIs(t, err, ramp.ErrOutcomeUnknown)
	assert.NotErrorIs(t, err, ramp.ErrRequestNotSent)
}

func TestCreateOnramp_RejectionsKeepTheirMeaning(t *testing.T) {
	cases := map[string]struct {
		answer stubAnswer
		want   error
	}{
		"key in flight":    {stubAnswer{http.StatusConflict, `{"message":"idempotency_key_in_flight"}`}, ramp.ErrRetryableUpstream},
		"payload mismatch": {stubAnswer{http.StatusUnprocessableEntity, `{"message":"idempotency_key_payload_mismatch"}`}, ramp.ErrIdempotencyConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{"POST payins/evm": tc.answer})
			_, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestGetOrder_HappyPathReadsTheCanonicalStatusAndHash(t *testing.T) {
	read := payinBody(t, map[string]any{
		"status":            "completed",
		"tracking_payment":  map[string]any{"step": "completed"},
		"tracking_complete": map[string]any{"step": "completed", "transaction_hash": "5hashsolana"},
	})
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payins/" + payinID: {200, read},
	})

	order, err := adapter.GetOrder(context.Background(), payinRef, payinID)
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "GET "+instancePrefix+"payins/"+payinID, got[0].method+" "+got[0].path)
	assert.Equal(t, payinID, order.ProviderOrderID)
	assert.Equal(t, models.TxStatusCompleted, order.Status)
	assert.Equal(t, "5hashsolana", order.TxHash)
	assert.Equal(t, map[string]string{"source_amount": "100.55", "destination_amount": "19.60"}, order.Amounts)
	assert.JSONEq(t, read, string(order.Raw))
}

// The port names only the provider's order id, so the customer is what binds
// the read: another customer's payin answers nothing and is flagged.
func TestGetOrder_AnotherCustomersPayinIsRefusedWithoutAStatus(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payin %s is not customer %s's", payinID, customerID).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payins/" + payinID: {200, payinBody(t, map[string]any{"customer_id": "re_000000000002", "status": "completed"})},
	})

	order, err := adapter.GetOrder(context.Background(), payinRef, payinID)
	assert.Nil(t, order)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	assert.NotContains(t, err.Error(), "completed")
	logger.AssertExpectations(t)
}

// A payin off the mode's Solana leg is not the order the row describes: it
// answers no status and is flagged, like another customer's.
func TestGetOrder_APayinOffTheModesSolanaLegIsRefused(t *testing.T) {
	cases := map[string]struct {
		mode      string
		overrides map[string]any
	}{
		"another network":                 {models.ProviderModeSandbox, map[string]any{"network": "polygon"}},
		"mainnet on a sandbox instance":   {models.ProviderModeSandbox, map[string]any{"network": "solana", "token": "USDC"}},
		"another token":                   {models.ProviderModeSandbox, map[string]any{"token": "USDC"}},
		"the test token on production":    {models.ProviderModeProduction, map[string]any{"network": "solana", "token": "USDB"}},
		"devnet on a production instance": {models.ProviderModeProduction, map[string]any{"network": "solana_devnet", "token": "USDC"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logger := captureLog(t)
			logger.EXPECT().Errorf("ramp.attention: blindpay payin %s is not on the %s solana leg", payinID, tc.mode).Once()
			body := map[string]any{"status": "completed", "tracking_payment": map[string]any{"step": "completed"}}
			maps.Copy(body, tc.overrides)
			adapter, _, _ := payinServer(t, tc.mode, map[string]stubAnswer{
				"GET payins/" + payinID: {200, payinBody(t, body)},
			})

			order, err := adapter.GetOrder(context.Background(), payinRef, payinID)
			assert.Nil(t, order)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			logger.AssertExpectations(t)
		})
	}
}

// Production settles USDC and USDT each as itself on mainnet.
func TestGetOrder_AProductionPayinOnEitherMainnetTokenIsRead(t *testing.T) {
	for _, token := range []string{"USDC", "USDT"} {
		t.Run(token, func(t *testing.T) {
			adapter, _, _ := payinServer(t, models.ProviderModeProduction, map[string]stubAnswer{
				"GET payins/" + payinID: {200, payinBody(t, map[string]any{"network": "solana", "token": token})},
			})
			order, err := adapter.GetOrder(context.Background(), payinRef, payinID)
			require.NoError(t, err)
			assert.Equal(t, models.TxStatusAwaitingFunds, order.Status)
		})
	}
}

// A failed payin whose Pix was received holds the user's money: every read of
// it is flagged for an operator.
func TestGetOrder_RefundAttentionIsLogged(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payin %s %s", payinID, models.TxStatusRefundAttention).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payins/" + payinID: {200, payinBody(t, map[string]any{
			"status": "failed", "tracking_payment": map[string]any{"step": "completed"},
		})},
	})

	order, err := adapter.GetOrder(context.Background(), payinRef, payinID)
	require.NoError(t, err)
	assert.Equal(t, models.TxStatusRefundAttention, order.Status)
	logger.AssertExpectations(t)
}

func TestGetOrder_RefusesLocallyWithoutCalling(t *testing.T) {
	cases := map[string]struct {
		ref  ramp.CustomerRef
		id   string
		want error
	}{
		"no customer": {ramp.CustomerRef{}, payinID, ramp.ErrIntegrationRequired},
		"no id":       {payinRef, "", ramp.ErrRequestNotSent},
		"path escape": {payinRef, "../customers", ramp.ErrRequestNotSent},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
			_, err := adapter.GetOrder(context.Background(), tc.ref, tc.id)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, calls())
		})
	}
}

func TestGetOrder_UnreadableAnswerFails(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET payins/" + payinID: {200, `{"id":`},
	})
	_, err := adapter.GetOrder(context.Background(), payinRef, payinID)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

// The payin exists but cannot be paid: its id is in the error and the log so
// the row can be reconciled.
func TestCreateOnramp_NoPixCodeNamesThePayin(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payin %s has no pix code", payinID).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, payinBody(t, map[string]any{"pix_code": nil})},
	})
	_, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	assert.Contains(t, err.Error(), payinID)
	logger.AssertExpectations(t)
}

// A created payin naming another customer is flagged and refused, but as a
// provider failure: something was created, so the row must stay.
func TestCreateOnramp_AnotherCustomersPayinIsRefusedAndFlagged(t *testing.T) {
	logger := captureLog(t)
	logger.EXPECT().Errorf("ramp.attention: blindpay payin %s is not customer %s's", payinID, customerID).Once()
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, payinBody(t, map[string]any{"customer_id": "re_000000000002"})},
	})
	order, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	assert.Nil(t, order)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	assert.NotErrorIs(t, err, ramp.ErrRequestNotSent)
	logger.AssertExpectations(t)
}

func TestCreateOnramp_AbsentCustomerIDIsAccepted(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST payins/evm": {200, payinBody(t, map[string]any{"customer_id": nil})},
	})
	_, err := adapter.CreateOnramp(context.Background(), payinRef, onrampCreate())
	require.NoError(t, err)
}
