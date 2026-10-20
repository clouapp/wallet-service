package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// signDelivery signs one delivery under the config's secret at timestamp (unix
// seconds). legacy is the X-Vault-Signature value, the hex HMAC-SHA256 of the body
// alone. timestamped is the X-Vault-Signature-V2 value,
// "t=<timestamp>,v1=<hex HMAC-SHA256 of "<timestamp>.<body>">": a receiver that
// checks t against its clock refuses a captured delivery replayed later. Every
// delivery is signed when it is posted, so a retry, a re-dispatch and a redelivery
// each carry the time of their own send. Neither value contains the secret.
func signDelivery(secret string, body []byte, timestamp int64) (legacy, timestamped string) {
	t := strconv.FormatInt(timestamp, 10)
	signed := []byte(t + "." + string(body))
	return hmacSHA256Hex(secret, body), "t=" + t + ",v1=" + hmacSHA256Hex(secret, signed)
}

func hmacSHA256Hex(secret string, message []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}
