package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// HMACAuth validates X-API-Key + X-API-Signature + X-API-Timestamp.
// Signature = HMAC-SHA256(secret, timestamp + method + path + body).
func HMACAuth(apiKeySecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		sig := c.GetHeader("X-API-Signature")
		ts := c.GetHeader("X-API-Timestamp")

		if apiKey == "" || sig == "" || ts == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth headers"})
			return
		}

		// Validate API key
		if apiKey != apiKeySecret {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		// Validate timestamp (5 min window)
		tsInt, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || math.Abs(float64(time.Now().Unix()-tsInt)) > 300 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "timestamp expired"})
			return
		}

		// Validate HMAC
		body, _ := c.GetRawData()
		message := fmt.Sprintf("%s%s%s%s", ts, c.Request.Method, c.Request.URL.Path, string(body))
		mac := hmac.New(sha256.New, []byte(apiKeySecret))
		mac.Write([]byte(message))
		expected := hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(sig), []byte(expected)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		c.Set("api_key", apiKey)
		c.Set("request_body", body)
		c.Next()
	}
}

// RequestLogger logs method, path, status, and duration as structured JSON.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		fmt.Printf(`{"level":"info","msg":"request","method":"%s","path":"%s","status":%d,"duration_ms":%d}`+"\n",
			c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start).Milliseconds())
	}
}
