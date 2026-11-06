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

func TestLiFi_Quote_RefusesSourcesCustodyCannotFundWithoutCallingLiFi(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("LI.FI must not be contacted for an unexecutable source: %s", r.URL.Path)
	}))
	defer server.Close()
	adapter := NewLiFi(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	for _, from := range []swap.Asset{
		{Symbol: "BTC", Chain: "btc", Decimals: 8, Native: true},
		{Symbol: "LTC", Chain: "ltc", Decimals: 8, Native: true},
		{Symbol: "XRP", Chain: "xrp", Decimals: 6, Native: true},
		{Symbol: "TRX", Chain: "tron", NetworkID: 728126428, Decimals: 6, Native: true},
	} {
		request := providerRequest()
		request.From = from
		_, err := adapter.Quote(context.Background(), request, swap.ProviderConfig{Enabled: true})
		require.ErrorIs(t, err, swap.ErrUnsupported, from.Chain)
	}
}

func TestLiFi_Status_PartialDeliveryIsNeverCompleted(t *testing.T) {
	request := providerRequest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"DONE","substatus":"PARTIAL","receiving":{"txHash":"0xreceive","amount":"5","toAddress":%q,"token":{"address":"0x9999999999999999999999999999999999999999","chainId":1,"decimals":6}}}`, request.ToAddress)
	}))
	defer server.Close()
	adapter := NewLiFi(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	payload, err := json.Marshal(lifiStep{ID: "route"})
	require.NoError(t, err)
	progress, err := adapter.Status(context.Background(), &swap.Quote{RouteID: "route", Request: request, Payload: payload}, "0xsend", swap.ProviderConfig{})
	require.NoError(t, err)
	require.Equal(t, swap.StatusPartial, progress.Status)
}

func swapKitServer(t *testing.T, routes string, build string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/quote":
			fmt.Fprint(w, `{"routes":[`+routes+`]}`)
		case "/v3/swap":
			fmt.Fprint(w, build)
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestSwapKit_Quote_SkipsMemoRoutesCustodyCannotSendFromXRP(t *testing.T) {
	request := providerRequest()
	request.From = swap.Asset{Symbol: "XRP", Chain: "xrp", Decimals: 6, Native: true}
	request.FromAddress = "rSourceAddress"
	expiration := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	route := func(id, provider string) string {
		return fmt.Sprintf(`{"routeId":%q,"providers":[%q],"sellAsset":"XRP.XRP","buyAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","sellAmount":"1","expectedBuyAmount":"2","expectedBuyAmountMaxSlippage":"1.9","expiration":%q}`, id, provider, expiration)
	}
	server := swapKitServer(t, route("thor", "THORCHAIN")+","+route("near", "NEAR"), "{}")
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	request.AmountRaw = "1000000"
	quote, err := adapter.Quote(context.Background(), request, swap.ProviderConfig{Enabled: true})
	require.NoError(t, err)
	require.Equal(t, "near", quote.RouteID)
}

func TestSwapKit_Build_RefusesATransactionFamilyOfAnotherChain(t *testing.T) {
	request := providerRequest()
	expiration := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	route := fmt.Sprintf(`{"routeId":"r","sellAsset":"ETH.ETH","buyAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","sellAmount":"1","expectedBuyAmount":"2000","expectedBuyAmountMaxSlippage":"1980","expiration":%q,"sourceAddress":%q,"destinationAddress":%q,"txType":"SERIALIZED_BASE64","tx":"AQID"}`, expiration, request.FromAddress, request.ToAddress)
	server := swapKitServer(t, route, route)
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	quote, err := adapter.Quote(context.Background(), request, swap.ProviderConfig{Enabled: true})
	require.NoError(t, err)
	_, err = adapter.Build(context.Background(), quote, swap.ProviderConfig{Enabled: true})
	require.ErrorIs(t, err, swap.ErrUnsupported)
}

func TestSwapKit_Build_RefusesAnXRPPaymentThatNeedsAMemo(t *testing.T) {
	request := providerRequest()
	request.From = swap.Asset{Symbol: "XRP", Chain: "xrp", Decimals: 6, Native: true}
	request.FromAddress, request.AmountRaw = "rSourceAddress", "1000000"
	expiration := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	route := fmt.Sprintf(`{"routeId":"r","providers":["NEAR"],"sellAsset":"XRP.XRP","buyAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","sellAmount":"1","expectedBuyAmount":"2","expectedBuyAmountMaxSlippage":"1.9","expiration":%q,"destinationAddress":%q,"txType":"RIPPLE","inboundAddress":"rDeposit","tx":{"Destination":"rDeposit","Memos":[{"Memo":{"MemoData":"3D3A"}}]}}`, expiration, request.ToAddress)
	server := swapKitServer(t, route, route)
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	quote, err := adapter.Quote(context.Background(), request, swap.ProviderConfig{Enabled: true})
	require.NoError(t, err)
	_, err = adapter.Build(context.Background(), quote, swap.ProviderConfig{Enabled: true})
	require.ErrorIs(t, err, swap.ErrUnsupported)
}

func TestSwapKit_Quote_AnXRPRouteWithoutProvidersIsNotOffered(t *testing.T) {
	request := providerRequest()
	request.From = swap.Asset{Symbol: "XRP", Chain: "xrp", Decimals: 6, Native: true}
	request.FromAddress, request.AmountRaw = "rSourceAddress", "1000000"
	expiration := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	route := fmt.Sprintf(`{"routeId":"r","sellAsset":"XRP.XRP","buyAsset":"ETH.USDC-0x1111111111111111111111111111111111111111","sellAmount":"1","expectedBuyAmount":"2","expectedBuyAmountMaxSlippage":"1.9","expiration":%q}`, expiration)
	server := swapKitServer(t, route, "{}")
	defer server.Close()
	adapter := NewSwapKit(Deps{Client: httpclient.Wrap(server.Client()), BaseURL: server.URL})
	_, err := adapter.Quote(context.Background(), request, swap.ProviderConfig{Enabled: true})
	require.ErrorIs(t, err, swap.ErrNoRoute)
}
