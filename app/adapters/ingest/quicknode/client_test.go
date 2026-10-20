package quicknode

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/ingest/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// computeQuickNodeSignature signs a delivery as QuickNode Streams does: HMAC-SHA256,
// keyed with the stream's security token, of nonce + timestamp + body, in hex.
func computeQuickNodeSignature(nonce, timestamp string, body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce + timestamp))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// signedHeaders are the headers of one signed delivery.
func signedHeaders(signatureHeader, nonce, timestamp string, body []byte, secret string) providers.Header {
	h := providers.Header{}
	h.Set("X-QN-Nonce", nonce)
	h.Set("X-QN-Timestamp", timestamp)
	h.Set(signatureHeader, computeQuickNodeSignature(nonce, timestamp, body, secret))
	return h
}

// ---------------------------------------------------------------------------
// buildFilterFunction
// ---------------------------------------------------------------------------

func TestBuild_FilterFunction_ValidBase64AndAddressesEmbedded(t *testing.T) {
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

func TestBuild_FilterFunction_SkipsEmptyStrings(t *testing.T) {
	b64 := buildFilterFunction([]string{"bc1qonly", "", "  "})
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	js := string(raw)
	assert.Contains(t, js, `"bc1qonly":true`)
	assert.NotContains(t, js, `""`)
}

func TestBuild_FilterFunction_EmptyList(t *testing.T) {
	b64 := buildFilterFunction(nil)
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "var addresses = {}")
}

// ---------------------------------------------------------------------------
// ParsePayload
// ---------------------------------------------------------------------------

func TestParse_Payload_SampleBTCTransaction(t *testing.T) {
	payload := []byte(`[{
		"txid": "abc123def456",
		"blockNumber": 850000,
		"blockHash": "0000000000000000000123456789abcdef",
		"toAddress": "bc1qxyz789",
		"vout": 2,
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
	assert.Equal(t, 2, tx.LogIndex, "an output is at its vout, as the scanner numbers it")
	assert.Nil(t, tx.Token)
	assert.True(t, time.Unix(1679000000, 0).Equal(tx.Timestamp))

	assert.True(t, tx.AmountIsHuman)
	assert.Equal(t, "0.5", tx.HumanAmount)
	assert.Nil(t, tx.Amount)
}

func TestParse_Payload_MultipleItems(t *testing.T) {
	payload := []byte(`[
		{"txid":"a","blockNumber":1,"blockHash":"h1","toAddress":"bc1qa","vout":0,"amount":0.00000001,"timestamp":100},
		{"txid":"b","blockNumber":2,"blockHash":"h2","toAddress":"bc1qb","vout":1,"amount":1,"timestamp":200}
	]`)
	p := NewQuickNodeProvider("k")
	out, err := p.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "0.00000001", out[0].HumanAmount)
	assert.Equal(t, "1", out[1].HumanAmount)
	assert.True(t, out[0].AmountIsHuman)
	assert.True(t, out[1].AmountIsHuman)
}

func TestParse_Payload_InvalidJSON(t *testing.T) {
	p := NewQuickNodeProvider("k")
	_, err := p.ParsePayload([]byte(`not json`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParse_Payload_SkipsAnItemItCannotReadAndKeepsTheOthers(t *testing.T) {
	for name, bad := range map[string]string{
		"empty txid":    `{"txid":"","blockNumber":1,"blockHash":"h","toAddress":"bc1q","vout":0,"amount":1,"timestamp":1}`,
		"missing vout":  `{"txid":"a","blockNumber":1,"blockHash":"h","toAddress":"bc1qa","amount":1,"timestamp":1}`,
		"negative vout": `{"txid":"a","blockNumber":1,"blockHash":"h","toAddress":"bc1qa","vout":-1,"amount":1,"timestamp":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			payload := []byte(`[` + bad + `,{"txid":"b","blockNumber":1,"blockHash":"h","toAddress":"bc1qb","vout":1,"amount":1,"timestamp":1}]`)

			out, err := NewQuickNodeProvider("k").ParsePayload(payload)
			require.NoError(t, err, "without its vout an output has no position; the others keep theirs")
			require.Len(t, out, 1)
			assert.Equal(t, "b", out[0].TxHash)
		})
	}
}

func TestParse_Payload_TwoOutputsOfOneTransactionKeepTheirVouts(t *testing.T) {
	payload := []byte(`[
		{"txid":"a","blockNumber":1,"blockHash":"h","toAddress":"bc1qa","vout":0,"amount":0.1,"timestamp":1},
		{"txid":"a","blockNumber":1,"blockHash":"h","toAddress":"bc1qa","vout":3,"amount":0.1,"timestamp":1}
	]`)

	out, err := NewQuickNodeProvider("k").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, []int{0, 3}, []int{out[0].LogIndex, out[1].LogIndex})
}

func TestBuild_FilterFunction_ReportsEachPaidOutputWithItsVout(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(buildFilterFunction([]string{"bc1qa"}))
	require.NoError(t, err)

	assert.Contains(t, string(raw), "vout: j", "the output index travels with the transfer")
	assert.Contains(t, string(raw), "vout.value > 0", "an empty output is not a transfer, as for the scanner")
}

// ---------------------------------------------------------------------------
// VerifyInbound
// ---------------------------------------------------------------------------

func TestQuick_NodeVerifyInbound_ValidSignature(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_security_token"

	ok, err := p.VerifyInbound(signedHeaders("X-QN-Signature", "nonce-1", now(), body, secret), body, secret)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestQuick_NodeVerifyInbound_TheNonceAndTimestampAreSigned(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_security_token"
	headers := signedHeaders("X-QN-Signature", "nonce-1", now(), body, secret)

	headers.Set("X-QN-Timestamp", later())
	ok, err := p.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, ok, "a replayed body under another timestamp is refused")

	bodyOnly := providers.Header{}
	bodyOnly.Set("X-QN-Nonce", "nonce-1")
	bodyOnly.Set("X-QN-Timestamp", now())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	bodyOnly.Set("X-QN-Signature", hex.EncodeToString(mac.Sum(nil)))
	ok, err = p.VerifyInbound(bodyOnly, body, secret)
	require.NoError(t, err)
	assert.False(t, ok, "a signature of the body alone is not QuickNode's")
}

func TestQuick_NodeVerifyInbound_InvalidSignature(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_security_token"
	headers := signedHeaders("X-QN-Signature", "nonce-1", now(), body, "another-token")

	ok, err := p.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestQuick_NodeVerifyInbound_MissingHeader(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[]`)
	full := signedHeaders("X-QN-Signature", "nonce-1", now(), body, "secret")
	for _, header := range []string{"X-QN-Signature", "X-QN-Nonce", "X-QN-Timestamp"} {
		h := providers.Header{}
		for name, values := range full {
			h[name] = values
		}
		delete(h, http.CanonicalHeaderKey(header))

		ok, err := p.VerifyInbound(h, body, "secret")
		assert.False(t, ok, header)
		require.Error(t, err, header)
		assert.Contains(t, err.Error(), "missing", header)
	}
}

func TestQuick_NodeVerifyInbound_CustomSignatureHeader(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	p.SetSignatureHeader("X-Custom-Sig")
	body := []byte(`{}`)
	secret := "s"

	ok, err := p.VerifyInbound(signedHeaders("X-Custom-Sig", "n", now(), body, secret), body, secret)
	require.NoError(t, err)
	assert.True(t, ok)
}

// now and later are delivery timestamps inside the replay window.
func now() string   { return strconv.FormatInt(time.Now().Unix(), 10) }
func later() string { return strconv.FormatInt(time.Now().Unix()+1, 10) }

func TestQuick_NodeVerifyInbound_RefusesADeliveryOutsideTheReplayWindow(t *testing.T) {
	p := NewQuickNodeProvider("api-key")
	body := []byte(`[{"txid":"x"}]`)
	secret := "qn_security_token"
	for name, timestamp := range map[string]string{
		"ten minutes old":   strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10),
		"ten minutes ahead": strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10),
		"not a time":        "yesterday",
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := p.VerifyInbound(signedHeaders("X-QN-Signature", "nonce-1", timestamp, body, secret), body, secret)
			assert.False(t, ok, "a signed delivery replayed later is refused")
			assert.Error(t, err)
		})
	}
}
