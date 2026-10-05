package coingecko

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestNewCoinGeckoProviderSelectsTheHost(t *testing.T) {
	free := NewCoinGeckoProvider("")
	if free.baseURL != publicBaseURL || free.apiKey != "" || free.client == nil {
		t.Fatal("an empty key did not select the public CoinGecko host")
	}
	pro := NewCoinGeckoProvider("present")
	if pro.baseURL != proBaseURL || pro.apiKey == "" || pro.client == nil {
		t.Fatal("a key did not select the pro CoinGecko host")
	}
	if free.Name() != "coingecko" || pro.Name() != "coingecko" {
		t.Fatal("CoinGecko provider name changed")
	}
}

func TestFetchCryptoPricesSkipsUnknownCodesWithoutHTTP(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	provider := NewCoinGeckoProvider("")
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	prices, err := provider.FetchCryptoPrices([]string{"NOTACOIN"})
	if err != nil {
		t.Fatal("unknown codes failed the quote")
	}
	if len(prices) != 0 || called {
		t.Fatal("unknown codes called CoinGecko or returned a price")
	}
}

func TestFetchCryptoPricesReadsTheUSDQuote(t *testing.T) {
	const proKey = "cg-pro-not-logged"
	var sawKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/simple/price" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("ids") != "bitcoin" || r.URL.Query().Get("vs_currencies") != "usd" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("Accept header was not application/json")
		}
		sawKey = r.Header.Get("x-cg-pro-api-key") == proKey
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bitcoin":{"usd":50000.5},"unknown-coin":{"usd":9},"matic-network":{"usd":0}}`))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinGeckoProvider(proKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	prices, err := provider.FetchCryptoPrices([]string{"btc"})
	if err != nil {
		t.Fatal("crypto quote failed")
	}
	if !sawKey {
		t.Fatal("pro request did not send the configured key")
	}
	got, ok := prices["BTC"]
	if !ok || !got.Equal(decimal.RequireFromString("50000.5")) {
		t.Fatalf("BTC price = %s", got)
	}
	if _, ok := prices["MATIC"]; ok {
		t.Fatal("a non-positive price was kept")
	}
	if len(prices) != 1 {
		t.Fatalf("price count = %d", len(prices))
	}
}

func TestFetchFiatRatesInvertsTheUSDCQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-cg-pro-api-key") != "" {
			t.Error("public request sent a pro key")
		}
		if r.URL.Query().Get("ids") != "usd-coin" || r.URL.Query().Get("vs_currencies") != "eur,brl" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usd-coin":{"eur":5.1,"brl":0}}`))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinGeckoProvider("")
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	rates, err := provider.FetchFiatRates([]string{"EUR", "BRL"})
	if err != nil {
		t.Fatal("fiat quote failed")
	}
	want := price.InvertRate(decimal.RequireFromString("5.1"))
	got, ok := rates["EUR"]
	if !ok || !got.Equal(want) {
		t.Fatalf("EUR rate = %s", got)
	}
	if _, ok := rates["BRL"]; ok || len(rates) != 1 {
		t.Fatal("a non-positive fiat quote was kept")
	}
}

func TestDoGetOmitsTheKeyFromAStatusError(t *testing.T) {
	const proKey = "cg-pro-not-logged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinGeckoProvider(proKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	_, err := provider.FetchCryptoPrices([]string{"ETH"})
	if err == nil {
		t.Fatal("a non-200 response was accepted")
	}
	if !strings.Contains(err.Error(), "status 429") || strings.Contains(err.Error(), proKey) {
		t.Fatal("status error did not keep the status, or it included the key")
	}
}
