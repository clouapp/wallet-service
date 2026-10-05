package coinmarketcap

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestNewCoinMarketCapProviderUsesTheRESTHost(t *testing.T) {
	provider := NewCoinMarketCapProvider("")
	if provider.baseURL != restBaseURL || provider.apiKey != "" || provider.client == nil {
		t.Fatal("an empty key did not build the CoinMarketCap REST client")
	}
	if provider.Name() != "coinmarketcap" {
		t.Fatal("CoinMarketCap provider name changed")
	}
}

func TestFetchCryptoPricesRequiresAKeyBeforeHTTP(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	provider := NewCoinMarketCapProvider("")
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	if _, err := provider.FetchCryptoPrices([]string{"BTC"}); err == nil || called {
		t.Fatal("a missing key called CoinMarketCap or was accepted")
	}
	if _, err := provider.FetchFiatRates([]string{"EUR"}); err == nil || called {
		t.Fatal("fiat rates called CoinMarketCap or were accepted")
	}
}

func TestFetchCryptoPricesReadsTheUSDQuote(t *testing.T) {
	const restKey = "cmc-rest-not-logged"
	var sawKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cryptocurrency/quotes/latest" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "BTC,MATIC,ETH" || r.URL.Query().Get("convert") != "USD" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("Accept header was not application/json")
		}
		sawKey = r.Header.Get("X-CMC_PRO_API_KEY") == restKey
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"BTC":{"quote":{"USD":{"price":50000.5}}},"ETH":{"quote":{"USD":{"price":0}}},"MATIC":{"quote":{"USD":{"price":0.8}}},"DOGE":{"quote":{"USD":{"price":1}}}}}`))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinMarketCapProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	prices, err := provider.FetchCryptoPrices([]string{"btc", "matic", "eth"})
	if err != nil {
		t.Fatal("crypto quote failed")
	}
	if !sawKey {
		t.Fatal("REST request did not send the configured key")
	}
	got, ok := prices["BTC"]
	if !ok || !got.Equal(decimal.RequireFromString("50000.5")) {
		t.Fatalf("BTC price = %s", got)
	}
	matic, ok := prices["MATIC"]
	if !ok || !matic.Equal(decimal.RequireFromString("0.8")) {
		t.Fatalf("MATIC price = %s", matic)
	}
	if _, ok := prices["ETH"]; ok {
		t.Fatal("a non-positive price was kept")
	}
	if _, ok := prices["DOGE"]; ok || len(prices) != 2 {
		t.Fatal("a symbol that was not requested was kept")
	}
}

func TestFetchFiatRatesDoesNotCallHTTP(t *testing.T) {
	const restKey = "cmc-rest-not-logged"
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	provider := NewCoinMarketCapProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	_, err := provider.FetchFiatRates([]string{"EUR"})
	if err == nil || called || !strings.Contains(err.Error(), "coinmarketcap: fiat rates not supported") || strings.Contains(err.Error(), restKey) {
		t.Fatal("fiat rates were accepted, called CoinMarketCap, or included the key")
	}
}

func TestGetOmitsTheKeyFromErrors(t *testing.T) {
	const restKey = "cmc-rest-not-logged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinMarketCapProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	_, err := provider.FetchCryptoPrices([]string{"ETH"})
	if err == nil {
		t.Fatal("a non-JSON body was accepted")
	}
	if !strings.Contains(err.Error(), "coinmarketcap parse") || strings.Contains(err.Error(), restKey) {
		t.Fatal("parse error did not keep the coinmarketcap label, or it included the key")
	}

	closed := server.URL
	server.Close()
	provider.baseURL = closed
	_, err = provider.FetchCryptoPrices([]string{"ETH"})
	if err == nil || !strings.Contains(err.Error(), "coinmarketcap:") || strings.Contains(err.Error(), restKey) {
		t.Fatal("transport error was not labeled, or it included the key")
	}
}
