package providers

import (
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// VerifyInbound
// ---------------------------------------------------------------------------

func TestHeliusVerifyInbound_ValidAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[{"signature":"abc"}]`)
	secret := "Bearer test-secret-value"

	headers := http.Header{}
	headers.Set("Authorization", secret)

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestHeliusVerifyInbound_InvalidAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[]`)
	secret := "Bearer correct"

	headers := http.Header{}
	headers.Set("Authorization", "Bearer wrong")

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestHeliusVerifyInbound_MissingAuthorization(t *testing.T) {
	provider := NewHeliusProvider("test-key")
	body := []byte(`[]`)

	headers := http.Header{}

	valid, err := provider.VerifyInbound(headers, body, "Bearer x")
	assert.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "missing Authorization")
}

// ---------------------------------------------------------------------------
// ParsePayload — native SOL (lamports)
// ---------------------------------------------------------------------------

func TestHeliusParsePayload_NativeSOLTransfer(t *testing.T) {
	ts := int64(1679000000)
	payload, err := json.Marshal([]map[string]interface{}{
		{
			"signature": "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW",
			"slot":      float64(123456789),
			"timestamp": ts,
			"nativeTransfers": []map[string]interface{}{
				{
					"fromUserAccount": "SenderPubkey1111111111111111111111111111111",
					"toUserAccount":   "ReceiverPubkey2222222222222222222222222222222",
					"amount":          float64(1_000_000_000),
				},
			},
		},
	})
	require.NoError(t, err)

	provider := NewHeliusProvider("k")
	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)

	tx := transfers[0]
	assert.Equal(t, "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW", tx.TxHash)
	assert.Equal(t, uint64(123456789), tx.BlockNumber)
	assert.Equal(t, "", tx.BlockHash)
	assert.Equal(t, "SenderPubkey1111111111111111111111111111111", tx.From)
	assert.Equal(t, "ReceiverPubkey2222222222222222222222222222222", tx.To)
	assert.Equal(t, "sol", tx.Asset)
	assert.Equal(t, -1, tx.LogIndex)
	assert.Nil(t, tx.Token)
	assert.Equal(t, 0, big.NewInt(1_000_000_000).Cmp(tx.Amount))
	assert.True(t, tx.Timestamp.Equal(time.Unix(ts, 0).UTC()))
}

// ---------------------------------------------------------------------------
// ParsePayload — SPL token transfer
// ---------------------------------------------------------------------------

func TestHeliusParsePayload_SPLTokenTransfer(t *testing.T) {
	dec := uint8(6)
	payload, err := json.Marshal([]map[string]interface{}{
		{
			"signature": "TokenTxSigBase58AAAAAAAAAAAAAAAAAAAAAAAAAAA",
			"slot":      float64(987654321),
			"timestamp": float64(1700000000),
			"tokenTransfers": []map[string]interface{}{
				{
					"fromUserAccount": "FromWalletAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					"toUserAccount":   "ToWalletBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
					"tokenAmount":     100.0,
					"mint":            "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
					"decimals":        dec,
				},
			},
		},
	})
	require.NoError(t, err)

	provider := NewHeliusProvider("k")
	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)

	tx := transfers[0]
	assert.Equal(t, "TokenTxSigBase58AAAAAAAAAAAAAAAAAAAAAAAAAAA", tx.TxHash)
	assert.Equal(t, uint64(987654321), tx.BlockNumber)
	assert.Equal(t, "FromWalletAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", tx.From)
	assert.Equal(t, "ToWalletBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", tx.To)
	assert.Equal(t, "spl", tx.Asset)
	assert.Equal(t, -1, tx.LogIndex)
	require.NotNil(t, tx.Token)
	assert.Equal(t, "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", tx.Token.Contract)
	assert.Equal(t, uint8(6), tx.Token.Decimals)
	assert.Equal(t, 0, big.NewInt(100_000_000).Cmp(tx.Amount))
}
