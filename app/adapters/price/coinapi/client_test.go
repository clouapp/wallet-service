package coinapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestNewCoinAPIProviderUsesTheRESTHost(t *testing.T) {
	provider := NewCoinAPIProvider("")
	if provider.baseURL != restBaseURL || provider.apiKey != "" || provider.client == nil {
		t.Fatal("an empty key did not build the CoinAPI REST client")
	}
	if provider.Name() != "coinapi" {
		t.Fatal("CoinAPI provider name changed")
	}
}

func TestFetchCryptoPricesRequiresAKeyBeforeHTTP(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	provider := NewCoinAPIProvider("")
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	if _, err := provider.FetchCryptoPrices(context.Background(), []string{"BTC"}); err == nil || called {
		t.Fatal("a missing key called CoinAPI or was accepted")
	}
	if _, err := provider.FetchFiatRates(context.Background(), []string{"EUR"}); err == nil {
		t.Fatal("a missing key was accepted for fiat")
	}
}

func TestFetchCryptoPricesReadsTheUSDQuote(t *testing.T) {
	const restKey = "coinapi-rest-not-logged"
	var sawKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/exchangerate/USD" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("invert") != "true" || r.URL.Query().Get("filter_asset_id") != "BTC,ETH" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		sawKey = r.Header.Get("X-CoinAPI-Key") == restKey
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rates":[{"asset_id_quote":"BTC","rate":"50000.5"},{"asset_id_quote":"ETH","rate":"0"}]}`))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinAPIProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	prices, err := provider.FetchCryptoPrices(context.Background(), []string{"btc", "eth"})
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
	if _, ok := prices["ETH"]; ok || len(prices) != 1 {
		t.Fatal("a non-positive rate was kept")
	}
}

func TestFetchFiatRatesInvertsTheQuote(t *testing.T) {
	const restKey = "coinapi-rest-not-logged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("invert") != "true" || r.URL.Query().Get("filter_asset_id") != "eur,BRL" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-CoinAPI-Key") != restKey {
			t.Error("fiat request did not send the configured key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rates":[{"asset_id_quote":"eur","rate":"5.1"},{"asset_id_quote":"BRL","rate":"0"}]}`))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinAPIProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	rates, err := provider.FetchFiatRates(context.Background(), []string{"eur", "BRL"})
	if err != nil {
		t.Fatal("fiat quote failed")
	}
	want := price.InvertRate(decimal.RequireFromString("5.1"))
	got, ok := rates["eur"]
	if !ok || !got.Equal(want) {
		t.Fatalf("eur rate = %s", got)
	}
	if _, ok := rates["BRL"]; ok || len(rates) != 1 {
		t.Fatal("a non-positive fiat quote was kept")
	}
}

func TestGetOmitsTheKeyFromErrors(t *testing.T) {
	const restKey = "coinapi-rest-not-logged"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	t.Cleanup(server.Close)

	provider := NewCoinAPIProvider(restKey)
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	_, err := provider.FetchCryptoPrices(context.Background(), []string{"ETH"})
	if err == nil {
		t.Fatal("a non-JSON body was accepted")
	}
	if !strings.Contains(err.Error(), "coinapi parse") || strings.Contains(err.Error(), restKey) {
		t.Fatal("parse error did not keep the coinapi label, or it included the key")
	}

	closed := server.URL
	server.Close()
	provider.baseURL = closed
	_, err = provider.FetchFiatRates(context.Background(), []string{"EUR"})
	if !errors.Is(err, chain.ErrProviderUnavailable) || !strings.Contains(chain.CauseText(err), "coinapi fiat:") || strings.Contains(err.Error(), restKey) || strings.Contains(chain.CauseText(err), restKey) {
		t.Fatal("transport error was not the unavailable sentinel, or it included the key")
	}
}

func TestFetchCryptoPricesStopsWhenTheContextIsCanceled(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	provider := NewCoinAPIProvider("present")
	provider.baseURL = server.URL
	provider.client = httpclient.Wrap(server.Client())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.FetchCryptoPrices(ctx, []string{"BTC"}); err == nil || called {
		t.Fatal("a canceled context called CoinAPI or was accepted")
	}
	if _, err := provider.FetchFiatRates(nil, []string{"EUR"}); err == nil || called {
		t.Fatal("a nil context called CoinAPI or was accepted")
	}
}
