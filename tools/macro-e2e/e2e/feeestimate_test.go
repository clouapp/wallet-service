package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	feeTestWallet = "ea20eac6-8a21-4c33-abc7-0987e1bd8c9f"
	feeTestTo     = "0xbcbe9e1e98c76e3b8f688dbd7afe3303a6d1a91f"
	feeTestToken  = "markets-token-not-real"
)

func staticToken(context.Context) (string, error) { return feeTestToken, nil }

func TestFee_Estimate_SendsAReadOnlyGETWithTheBearerToken(t *testing.T) {
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fee":"0.0000021","fee_asset":"ETH"}`))
	}))
	defer server.Close()

	result, err := FeeEstimate(context.Background(), FeeEstimateRequest{WalletID: feeTestWallet, Asset: "USDC", To: feeTestTo, Amount: "0.5"},
		staticToken, APIClient{BaseURL: server.URL, HTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("status %d", result.Status)
	}
	if seen.Method != http.MethodGet || seen.URL.Path != "/api/v1/wallets/"+feeTestWallet+"/fee-estimate" {
		t.Fatalf("request %s %s", seen.Method, seen.URL.Path)
	}
	query := seen.URL.Query()
	if query.Get("asset") != "USDC" || query.Get("to") != feeTestTo || query.Get("amount") != "0.5" {
		t.Fatalf("query %v", query)
	}
	if seen.Header.Get("Authorization") != "Bearer "+feeTestToken {
		t.Fatal("bearer token not sent")
	}
	body, isObject := result.Body.(pyjson.Object)
	if !isObject {
		t.Fatalf("body %T", result.Body)
	}
	if fee, _ := body.Get("fee"); fee != "0.0000021" {
		t.Fatalf("fee %v", fee)
	}
}

func TestFee_Estimate_OmitsAnEmptyAmount(t *testing.T) {
	var rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result, err := FeeEstimate(context.Background(), FeeEstimateRequest{WalletID: feeTestWallet, Asset: "ETH", To: feeTestTo},
		staticToken, APIClient{BaseURL: server.URL, HTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusServiceUnavailable || strings.Contains(rawQuery, "amount") {
		t.Fatalf("status %d query %q", result.Status, rawQuery)
	}
}

func TestFee_Estimate_RejectsBadInputBeforeReadingTheToken(t *testing.T) {
	tokenRead := false
	token := func(context.Context) (string, error) { tokenRead = true; return feeTestToken, nil }
	bad := []FeeEstimateRequest{
		{WalletID: "not-a-uuid", Asset: "ETH", To: feeTestTo},
		{WalletID: feeTestWallet, Asset: "eth&x=1", To: feeTestTo},
		{WalletID: feeTestWallet, Asset: "ETH", To: "0x../../admin"},
		{WalletID: feeTestWallet, Asset: "ETH", To: feeTestTo, Amount: "1e5"},
		{WalletID: feeTestWallet, Asset: "ETH", To: feeTestTo, Amount: "-1"},
		{WalletID: feeTestWallet, Asset: "ETH", To: feeTestTo, Amount: "01.5"},
	}
	for _, request := range bad {
		if _, err := FeeEstimate(context.Background(), request, token, APIClient{BaseURL: "http://127.0.0.1:1"}); err == nil {
			t.Errorf("accepted %+v", request)
		}
	}
	if tokenRead {
		t.Fatal("token read for invalid input")
	}
}

func TestFee_Estimate_ReportsATokenFailure(t *testing.T) {
	failing := func(context.Context) (string, error) {
		return "", errors.New("could not read the Markets Macro Wallets API token")
	}
	_, err := FeeEstimate(context.Background(), FeeEstimateRequest{WalletID: feeTestWallet, Asset: "ETH", To: feeTestTo}, failing, APIClient{BaseURL: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "Markets Macro Wallets API token") {
		t.Fatalf("got %v", err)
	}
}
