package swapproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/stretchr/testify/require"
)

func providerRequest() swap.QuoteRequest {
	return swap.QuoteRequest{From: swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}, To: swap.Asset{Symbol: "USDC", Chain: "eth", NetworkID: 1, Decimals: 6, Contract: "0x1111111111111111111111111111111111111111"}, FromAddress: "0x2222222222222222222222222222222222222222", ToAddress: "0x3333333333333333333333333333333333333333", AmountRaw: "1000000000000000000", SlippageBPS: 100}
}

func TestLiFi_Quote_UsesExactUnitsAndVerifiesTheReturnedRoute(t *testing.T) {
	request := providerRequest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "lifi-fixture", r.Header.Get("x-lifi-api-key"))
		require.Equal(t, "MacroWallets-Swap/1.0", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chains":
			fmt.Fprint(w, `{"chains":[{"id":1,"key":"eth","nativeToken":{"address":"0x0000000000000000000000000000000000000000","symbol":"ETH","decimals":18}}]}`)
		case "/quote":
			require.Equal(t, request.AmountRaw, r.URL.Query().Get("fromAmount"))
			require.Equal(t, "0.0100", r.URL.Query().Get("slippage"))
			fmt.Fprintf(w, `{"id":"lifi-route","tool":"relay","action":{"fromChainId":1,"toChainId":1,"fromToken":{"address":"0x0000000000000000000000000000000000000000","decimals":18},"toToken":{"address":%q,"decimals":6},"fromAmount":%q,"fromAddress":%q,"toAddress":%q},"estimate":{"toAmount":"2000000000","toAmountMin":"1980000000","executionDuration":30},"transactionRequest":{"to":"0x4444444444444444444444444444444444444444","from":%q,"chainId":1,"value":"1000000000000000000","data":"0x1234"}}`, request.To.Contract, request.AmountRaw, request.FromAddress, request.ToAddress, request.FromAddress)
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	adapter := NewLiFi(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	credential := swap.ProviderConfig{Enabled: true, APIKey: "lifi-fixture"}
	quote, err := adapter.Quote(context.Background(), request, credential)
	require.NoError(t, err)
	require.NoError(t, quote.Validate(time.Now()))
	require.Equal(t, "1980000000", quote.MinimumRaw)
	funding, err := adapter.Build(context.Background(), quote, credential)
	require.NoError(t, err)
	require.Equal(t, "evm", funding.Kind)
	require.Equal(t, "0x1234", funding.Data)
	require.Equal(t, int64(1), funding.ChainID)
	var changed lifiStep
	require.NoError(t, json.Unmarshal(quote.Payload, &changed))
	changed.ID = "different-route"
	quote.Payload, _ = json.Marshal(changed)
	_, err = adapter.Build(context.Background(), quote, credential)
	require.ErrorIs(t, err, swap.ErrInvalidQuote)
}

func TestSwapKit_QuoteAndBuild_UseTheAcceptedRouteAndStrictSlippage(t *testing.T) {
	request := providerRequest()
	minimum := "1980"
	buildCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "swapkit-fixture", r.Header.Get("x-api-key"))
		require.Equal(t, "MacroWallets-Swap/1.0", r.Header.Get("User-Agent"))
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		route := fmt.Sprintf(`{"routeId":"swapkit-route","sellAsset":"ETH.ETH","buyAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","sellAmount":"1","expectedBuyAmount":"2000","expectedBuyAmountMaxSlippage":%q,"expiration":%q,"estimatedTime":{"total":12.5},"sourceAddress":%q,"destinationAddress":%q,"txType":"EVM","tx":{"to":"0x4444444444444444444444444444444444444444","value":"1000000000000000000","data":"0x1234"}}`, minimum, strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10), request.FromAddress, request.ToAddress)
		switch r.URL.Path {
		case "/v3/quote":
			require.JSONEq(t, `"1"`, string(body["sellAmount"]))
			require.JSONEq(t, `1.00`, string(body["slippage"]))
			fmt.Fprint(w, `{"routes":[`+route+`]}`)
		case "/v3/swap":
			buildCalls++
			require.JSONEq(t, `"swapkit-route"`, string(body["routeId"]))
			require.JSONEq(t, `"STRICT"`, string(body["slippageSetting"]))
			fmt.Fprint(w, route)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	credential := swap.ProviderConfig{Enabled: true, APIKey: "swapkit-fixture"}
	quote, err := adapter.Quote(context.Background(), request, credential)
	require.NoError(t, err)
	require.Equal(t, "1980000000", quote.MinimumRaw)
	require.Equal(t, 13, quote.EstimatedSeconds)
	funding, err := adapter.Build(context.Background(), quote, credential)
	require.NoError(t, err)
	require.Equal(t, "evm", funding.Kind)
	minimum = "1979"
	_, err = adapter.Build(context.Background(), quote, credential)
	require.ErrorIs(t, err, swap.ErrExpired)
	require.Equal(t, 2, buildCalls)
}

func TestSwapKit_Quote_ParsesLiveAndLegacyExpirationFormatsAndRejectsMalformedValues(t *testing.T) {
	expiry := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{"unix seconds", strconv.FormatInt(expiry.Unix(), 10), true},
		{"RFC3339", expiry.Format(time.RFC3339), true},
		{"malformed", "tomorrow", false},
		{"overflow", "999999999999999999999999", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := providerRequest()
			route := swapKitRoute{RouteID: "swapkit-route", SellAsset: "ETH.ETH", BuyAsset: "ETH.USDC-" + request.To.Contract,
				SellAmount: "1", Expected: "2000", ExpectedMinimum: "1980", Expiration: test.value}
			route.Time.Total = json.Number("12.5")
			quote, err := NewSwapKit(Deps{}).normalize(route, request)
			if !test.valid {
				require.ErrorIs(t, err, swap.ErrInvalidQuote)
				return
			}
			require.NoError(t, err)
			require.NoError(t, quote.Validate(time.Now()))
			require.True(t, expiry.Equal(quote.ExpiresAt))
			require.Equal(t, 13, quote.EstimatedSeconds)
		})
	}
}

func TestProviderHTTP_ClassifiesAvailabilityWithoutExposingCredentialsOrErrorBodies(t *testing.T) {
	for _, test := range []struct {
		status int
		want   error
	}{{429, swap.ErrProviderUnavailable}, {503, swap.ErrProviderUnavailable}, {404, swap.ErrNoRoute}, {403, swap.ErrProviderRejected}, {451, swap.ErrProviderRejected}, {200, swap.ErrInvalidQuote}} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, "provider-private-fixture")
			}))
			defer server.Close()
			adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
			_, err := adapter.Quote(context.Background(), providerRequest(), swap.ProviderConfig{APIKey: "private-key-fixture"})
			require.ErrorIs(t, err, test.want)
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestSwapKit_Status_ReadsDocumentedTopLevelLegsAndKeepsUnknownOutcomesPending(t *testing.T) {
	status := "completed"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/track", r.URL.Path)
		fmt.Fprintf(w, `{"status":%q,"trackingStatus":%q,"legs":[{"hash":"receiving-fixture","status":"completed","toAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","toAmount":"1990","toAddress":"0x3333333333333333333333333333333333333333"}]}`, status, status)
	}))
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	quote := &swap.Quote{RouteID: "route", Request: providerRequest()}
	progress, err := adapter.Status(context.Background(), quote, "source-fixture", swap.ProviderConfig{})
	require.NoError(t, err)
	require.Equal(t, swap.StatusCompleted, progress.Status)
	require.Equal(t, "1990000000", progress.ReceivedRaw)
	require.Equal(t, "receiving-fixture", progress.ReceivingHash)
	status = "unknown"
	progress, err = adapter.Status(context.Background(), quote, "source-fixture", swap.ProviderConfig{})
	require.NoError(t, err)
	require.Equal(t, swap.StatusReconciling, progress.Status)
}
