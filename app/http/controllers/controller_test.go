package controllers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/macromarkets/vault/app/http/middleware"
	"github.com/macromarkets/vault/app/providers"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/app/services/wallet"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/app/services/withdraw"
	"github.com/macromarkets/vault/app/services/deposit"
	"github.com/macromarkets/vault/pkg/types"
	"github.com/macromarkets/vault/tests/mocks"
)

const testSecret = "test-api-secret"

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestAPI(t *testing.T) (*gin.Engine, *providers.Container) {
	t.Helper()
	db := mocks.TestDB(t)

	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	registry.RegisterChain(mocks.NewMockChain("btc"))
	registry.RegisterChain(mocks.NewMockChain("sol"))
	registry.RegisterChain(mocks.NewMockChain("polygon"))
	registry.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6, Contract: "0xdAC17F"})
	registry.RegisterToken(types.Token{Symbol: "usdc", ChainID: "eth", Decimals: 6, Contract: "0xA0b869"})

	webhookSvc := webhook.NewService(db, nil)
	walletSvc := wallet.NewService(db, registry, nil)
	withdrawSvc := withdraw.NewService(db, registry, &queue.SQSClient{}, webhookSvc)
	depositSvc := deposit.NewService(db, nil, registry, webhookSvc)

	container := &providers.Container{
		DB: db, Registry: registry,
		WalletService: walletSvc, WithdrawalService: withdrawSvc,
		WebhookService: webhookSvc, DepositService: depositSvc,
	}

	r := gin.New()
	r.Use(gin.Recovery())
	v1 := r.Group("/v1")
	v1.Use(middleware.HMACAuth(testSecret))
	ctrl := New(container)
	ctrl.RegisterRoutes(v1)

	// Add unauthed health
	r.GET("/health", ctrl.health)

	return r, container
}

func authRequest(method, path, body string) *http.Request {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signReq(testSecret, ts, method, path, body)

	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("X-API-Key", testSecret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)
	return req
}

func signReq(secret, ts, method, path, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + method + path + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestHealth(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected ok, got %v", resp["status"])
	}
}

func TestListChains(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/chains", ""))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	if len(data) != 4 {
		t.Errorf("expected 4 chains, got %d", len(data))
	}
}

func TestCreateWallet_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	body := `{"chain":"eth","label":"Test ETH"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", "/v1/wallets", body))

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["chain"] != "eth" {
		t.Errorf("expected eth, got %v", resp["chain"])
	}
}

func TestCreateWallet_DuplicateChain_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	body := `{"chain":"eth","label":"first"}`

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, authRequest("POST", "/v1/wallets", body))
	if w1.Code != 201 {
		t.Fatalf("first create: expected 201, got %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, authRequest("POST", "/v1/wallets", body))
	if w2.Code != 409 {
		t.Errorf("duplicate create: expected 409, got %d", w2.Code)
	}
}

func TestCreateWallet_MissingChain_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", "/v1/wallets", `{"label":"no chain"}`))

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestListWallets_API(t *testing.T) {
	r, _ := setupTestAPI(t)

	r.ServeHTTP(httptest.NewRecorder(), authRequest("POST", "/v1/wallets", `{"chain":"eth"}`))
	r.ServeHTTP(httptest.NewRecorder(), authRequest("POST", "/v1/wallets", `{"chain":"btc"}`))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/wallets", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("expected 2 wallets, got %d", len(data))
	}
}

func TestGenerateAddress_API(t *testing.T) {
	r, _ := setupTestAPI(t)

	// Create wallet
	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, authRequest("POST", "/v1/wallets", `{"chain":"eth"}`))
	var walletResp map[string]interface{}
	json.Unmarshal(cw.Body.Bytes(), &walletResp)
	walletID := walletResp["id"].(string)

	// Generate address
	body := `{"external_user_id":"user_api_test"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", fmt.Sprintf("/v1/wallets/%s/addresses", walletID), body))

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["external_user_id"] != "user_api_test" {
		t.Errorf("expected user_api_test, got %v", resp["external_user_id"])
	}
	if resp["address"] == nil || resp["address"] == "" {
		t.Error("expected non-empty address")
	}
}

func TestGenerateAddress_MissingUserID_API(t *testing.T) {
	r, _ := setupTestAPI(t)

	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, authRequest("POST", "/v1/wallets", `{"chain":"eth"}`))
	var wr map[string]interface{}
	json.Unmarshal(cw.Body.Bytes(), &wr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", fmt.Sprintf("/v1/wallets/%s/addresses", wr["id"]), `{}`))
	if w.Code != 400 {
		t.Errorf("expected 400 for missing user_id, got %d", w.Code)
	}
}

func TestCreateWithdrawal_API(t *testing.T) {
	r, _ := setupTestAPI(t)

	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, authRequest("POST", "/v1/wallets", `{"chain":"eth"}`))
	var wr map[string]interface{}
	json.Unmarshal(cw.Body.Bytes(), &wr)
	walletID := wr["id"].(string)

	body := fmt.Sprintf(`{
		"external_user_id":"user_w",
		"to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		"amount":"1000000",
		"asset":"eth",
		"idempotency_key":"api_test_001"
	}`)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", fmt.Sprintf("/v1/wallets/%s/withdrawals", walletID), body))

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "pending" {
		t.Errorf("expected pending, got %v", resp["status"])
	}
}

func TestCreateWithdrawal_MissingFields_API(t *testing.T) {
	r, _ := setupTestAPI(t)

	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, authRequest("POST", "/v1/wallets", `{"chain":"eth"}`))
	var wr map[string]interface{}
	json.Unmarshal(cw.Body.Bytes(), &wr)
	walletID := wr["id"].(string)

	// Missing idempotency_key
	body := `{"external_user_id":"u","to_address":"0xabc","amount":"100","asset":"eth"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", fmt.Sprintf("/v1/wallets/%s/withdrawals", walletID), body))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestListTransactions_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/transactions", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestListTransactions_WithFilters_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/transactions?chain=eth&type=deposit&status=pending&limit=10", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCreateWebhook_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	body := `{"url":"https://myserver.com/wh","secret":"whsec","events":["deposit.confirmed","withdrawal.confirmed"]}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("POST", "/v1/webhooks", body))

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListWebhooks_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	r.ServeHTTP(httptest.NewRecorder(), authRequest("POST", "/v1/webhooks",
		`{"url":"https://a.com","secret":"s","events":["deposit.confirmed"]}`))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/webhooks", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	if len(data) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(data))
	}
}

func TestGetWallet_NotFound_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/wallets/00000000-0000-0000-0000-000000000000", ""))
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetWallet_InvalidUUID_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/wallets/not-a-uuid", ""))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLookupAddress_NotFound_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/addresses/0xnonexistent?chain=eth", ""))
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestListUserAddresses_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/users/user_nobody/addresses", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestListUserTransactions_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/users/user_nobody/transactions", ""))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetTransaction_NotFound_API(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authRequest("GET", "/v1/transactions/00000000-0000-0000-0000-000000000000", ""))
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUnauthenticatedRequest(t *testing.T) {
	r, _ := setupTestAPI(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/wallets", nil)
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401 for unauthenticated request, got %d", w.Code)
	}
}
