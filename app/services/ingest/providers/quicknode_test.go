package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func computeQuickNodeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// buildFilterFunction
// ---------------------------------------------------------------------------

func TestBuildFilterFunction_ValidBase64AndAddressesEmbedded(t *testing.T) {
	addrs := []string{"bc1qtestaddr1xxxxxxxxxxxxxxxxxxxx", "bc1qtestaddr2yyyyyyyyyyyyyyyyyyyy"}
	b64 := buildFilterFunction(addrs)
	require.NotEmpty(t, b64)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	js := string(raw)
	assert.Contains(t, js, "function main(stream)")
	assert.Contains(t, js, "bc1qtestaddr1xxxxxxxxxxxxxxxxxxxx")
	assert.Contains(t, js, "bc1qtestaddr2yyyyyyyyyyyyyyyyyyyy")
	assert.Contains(t, js, `"bc1qtestaddr1xxxxxxxxxxxxxxxxxxxx":true`)
	assert.Contains(t, js, `"bc1qtestaddr2yyyyyyyyyyyyyyyyyyyy":true`)
}

func TestBuildFilterFunction_SkipsEmptyStrings(t *testing.T) {
	b64 := buildFilterFunction([]string{"bc1qonly", "", "  "})
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	js := string(raw)
	assert.Contains(t, js, `"bc1qonly":true`)
	assert.NotContains(t, js, `""`)
}

func TestBuildFilterFunction_EmptyList(t *testing.T) {
	b64 := buildFilterFunction(nil)
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "var addresses = {}")
}

// ---------------------------------------------------------------------------
// ParsePayload
// ---------------------------------------------------------------------------

func TestParsePayload_SampleBTCTransaction(t *testing.T) {
	payload := []byte(`[{
		"txid": "abc123def456",
		"blockNumber": 850000,
		"blockHash": "0000000000000000000123456789abcdef",
		"toAddress": "bc1qxyz789",
		"amount": 0.5,
		"timestamp": 1679000000
	}]`)

	p := NewQuickNodeProvider("key")
	transfers, err := p.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)

	tx := transfers[0]
	assert.Equal(t, "abc123def456", tx.TxHash)
	assert.Equal(t, uint64(850000), tx.BlockNumber)
	assert.Equal(t, "0000000000000000000123456789abcdef", tx.BlockHash)
	assert.Equal(t, "", tx.From)
	assert.Equal(t, "bc1qxyz789", tx.To)
	assert.Equal(t, "BTC", tx.Asset)
	assert.Equal(t, -1, tx.LogIndex)
	assert.Nil(t, tx.Token)
	assert.True(t, time.Unix(1679000000, 0).Equal(tx.Timestamp))

	expected := big.NewInt(50_000_000)
	assert.Equal(t, 0, tx.Amount.Cmp(expected), "0.5 BTC = 50M satoshis")
}

func TestParsePayload_MultipleItems(t *testing.T) {
	payload := []byte(`[
		{"txid":"a","blockNumber":1,"blockHash":"h1","toAddress":"bc1qa","amount":0.00000001,"timestamp":100},
		{"txid":"b","blockNumber":2,"blockHash":"h2","toAddress":"bc1qb","amount":1,"timestamp":200}
	]`)
	p := NewQuickNodeProvider("k")
	out, err := p.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, int64(1), out[0].Amount.Int64())
	assert.Equal(t, int64(100_000_000), out[1].Amount.Int64())
}

func TestParsePayload_InvalidJSON(t *testing.T) {
	p := NewQuickNodeProvider("k")
	_, err := p.ParsePayload([]byte(`not json`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParsePayload_EmptyTxid(t *testing.T) {
	payload := []byte(`[{"txid":"","blockNumber":1,"blockHash":"h","toAddress":"bc1q","amount":1,"timestamp":1}]`)
	p := NewQuickNodeProvider("k")
	_, err := p.ParsePayload(payload)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "txid")
}

// ---------------------------------------------------------------------------
// VerifyInbound
// ---------------------------------------------------------------------------

func TestQuickNodeVerifyInbound_ValidSignature(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_webhook_secret"
	sig := computeQuickNodeSignature(body, secret)

	h := http.Header{}
	h.Set("X-QN-Signature", sig)

	ok, err := p.VerifyInbound(h, body, secret)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestQuickNodeVerifyInbound_InvalidSignature(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_webhook_secret"

	h := http.Header{}
	h.Set("X-QN-Signature", "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678")

	ok, err := p.VerifyInbound(h, body, secret)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestQuickNodeVerifyInbound_MissingHeader(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[]`)

	ok, err := p.VerifyInbound(http.Header{}, body, "secret")
	assert.False(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestQuickNodeVerifyInbound_CustomSignatureHeader(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	p.SetSignatureHeader("X-Custom-Sig")
	body := []byte(`{}`)
	secret := "s"
	sig := computeQuickNodeSignature(body, secret)

	h := http.Header{}
	h.Set("X-Custom-Sig", sig)

	ok, err := p.VerifyInbound(h, body, secret)
	require.NoError(t, err)
	assert.True(t, ok)
}
