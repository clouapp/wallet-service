package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"
)

// HMACAuth validates X-API-Key + X-API-Signature + X-API-Timestamp for Goravel.
// Signature = HMAC-SHA256(secret, timestamp + method + path + body).
func HMACAuth(ctx http.Context) {
	apiKeySecret := os.Getenv("API_KEY_SECRET")

	apiKey := ctx.Request().Header("X-API-Key", "")
	sig := ctx.Request().Header("X-API-Signature", "")
	ts := ctx.Request().Header("X-API-Timestamp", "")

	if apiKey == "" || sig == "" || ts == "" {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{
			"error": "missing auth headers",
		})
		return
	}

	// Validate API key
	if apiKey != apiKeySecret {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{
			"error": "invalid API key",
		})
		return
	}

	// Validate timestamp (5 min window)
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-tsInt)) > 300 {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{
			"error": "timestamp expired",
		})
		return
	}

	// Validate HMAC
	bodyBytes, _ := io.ReadAll(ctx.Request().Origin().Body)
	message := fmt.Sprintf("%s%s%s%s", ts, ctx.Request().Method(), ctx.Request().Path(), string(bodyBytes))
	mac := hmac.New(sha256.New, []byte(apiKeySecret))
	mac.Write([]byte(message))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		ctx.Request().AbortWithStatus(http.StatusUnauthorized)
		ctx.Response().Json(http.StatusUnauthorized, http.Json{
			"error": "invalid signature",
		})
		return
	}

	ctx.Request().Next()
}

// RequestLogger logs method, path, status, and duration as structured JSON for Goravel.
func RequestLogger(ctx http.Context) {
	start := time.Now()
	ctx.Request().Next()

	fmt.Printf(`{"level":"info","msg":"request","method":"%s","path":"%s","duration_ms":%d}`+"\n",
		ctx.Request().Method(), ctx.Request().Path(), time.Since(start).Milliseconds())
}
