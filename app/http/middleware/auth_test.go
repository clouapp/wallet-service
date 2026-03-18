package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func signRequest(secret, timestamp, method, path, body string) string {
	message := timestamp + method + path + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func setupAuthRouter(secret string) *gin.Engine {
	r := gin.New()
	r.Use(GinHMACAuth(secret))
	r.POST("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	return r
}

func TestHMACAuth_ValidRequest(t *testing.T) {
	secret := "test-secret"
	r := setupAuthRouter(secret)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	body := `{"chain":"eth"}`
	sig := signRequest(secret, ts, "POST", "/test", body)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHMACAuth_MissingHeaders(t *testing.T) {
	r := setupAuthRouter("secret")

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{"no headers", map[string]string{}},
		{"missing key", map[string]string{"X-API-Signature": "sig", "X-API-Timestamp": "123"}},
		{"missing sig", map[string]string{"X-API-Key": "key", "X-API-Timestamp": "123"}},
		{"missing ts", map[string]string{"X-API-Key": "key", "X-API-Signature": "sig"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Errorf("expected 401, got %d", w.Code)
			}
		})
	}
}

func TestHMACAuth_WrongAPIKey(t *testing.T) {
	r := setupAuthRouter("correct-secret")

	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := signRequest("correct-secret", ts, "GET", "/test", "")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHMACAuth_WrongSignature(t *testing.T) {
	secret := "test-secret"
	r := setupAuthRouter(secret)

	ts := fmt.Sprintf("%d", time.Now().Unix())

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", "invalid_signature_value")
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHMACAuth_ExpiredTimestamp(t *testing.T) {
	secret := "test-secret"
	r := setupAuthRouter(secret)

	// 10 minutes ago — outside 5 min window
	ts := fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix())
	sig := signRequest(secret, ts, "GET", "/test", "")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHMACAuth_FutureTimestamp(t *testing.T) {
	secret := "test-secret"
	r := setupAuthRouter(secret)

	// 10 minutes in the future
	ts := fmt.Sprintf("%d", time.Now().Add(10*time.Minute).Unix())
	sig := signRequest(secret, ts, "GET", "/test", "")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHMACAuth_InvalidTimestamp(t *testing.T) {
	r := setupAuthRouter("secret")

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("X-API-Signature", "sig")
	req.Header.Set("X-API-Timestamp", "not-a-number")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHMACAuth_WithBody(t *testing.T) {
	secret := "body-test"
	r := setupAuthRouter(secret)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	body := `{"to_address":"0xabc","amount":"100"}`
	sig := signRequest(secret, ts, "POST", "/test", body)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHMACAuth_TamperedBody(t *testing.T) {
	secret := "tamper-test"
	r := setupAuthRouter(secret)

	ts := fmt.Sprintf("%d", time.Now().Unix())
	originalBody := `{"amount":"100"}`
	sig := signRequest(secret, ts, "POST", "/test", originalBody)

	// Send different body
	tamperedBody := `{"amount":"999999"}`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(tamperedBody))
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-API-Signature", sig)
	req.Header.Set("X-API-Timestamp", ts)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401 for tampered body, got %d", w.Code)
	}
}
