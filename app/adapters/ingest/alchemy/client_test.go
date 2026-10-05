package alchemy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math/big"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/ingest/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func computeAlchemySignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// VerifyInbound
// ---------------------------------------------------------------------------

func TestVerifyInbound_ValidSignature(t *testing.T) {
	provider := NewAlchemyProvider("test-key")
	body := []byte(`{"event":"test"}`)
	secret := "whsec_test_secret"
	sig := computeAlchemySignature(body, secret)

	headers := providers.Header{}
	headers.Set("X-Alchemy-Signature", sig)

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.True(t, valid)
}

func TestVerifyInbound_InvalidSignature(t *testing.T) {
	provider := NewAlchemyProvider("test-key")
	body := []byte(`{"event":"test"}`)
	secret := "whsec_test_secret"

	headers := providers.Header{}
	headers.Set("X-Alchemy-Signature", "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678")

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestVerifyInbound_MissingHeader(t *testing.T) {
	provider := NewAlchemyProvider("test-key")
	body := []byte(`{"event":"test"}`)

	headers := providers.Header{}

	valid, err := provider.VerifyInbound(headers, body, "some-secret")
	assert.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "missing")
}

// ---------------------------------------------------------------------------
// ParsePayload — native ETH transfer (category: external)
// ---------------------------------------------------------------------------

func TestParsePayload_NativeETHTransfer(t *testing.T) {
	payload := []byte(`{
		"event": {
			"activity": [{
				"blockNum": "0x10f3c5a",
				"hash": "0xabc123def456789",
				"fromAddress": "0xSender",
				"toAddress": "0xReceiver",
				"value": 1.5,
				"asset": "ETH",
				"category": "external",
				"rawContract": {
					"rawValue": "0x14d1120d7b160000",
					"address": "",
					"decimals": 18
				},
				"log": {
					"logIndex": "",
					"blockHash": "0xblockhash123"
				}
			}]
		}
	}`)

	provider := NewAlchemyProvider("test-key")
	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)

	tx := transfers[0]
	assert.Equal(t, "0xabc123def456789", tx.TxHash)
	assert.Equal(t, uint64(0x10f3c5a), tx.BlockNumber)
	assert.Equal(t, "0xblockhash123", tx.BlockHash)
	assert.Equal(t, "0xSender", tx.From)
	assert.Equal(t, "0xReceiver", tx.To)
	assert.Equal(t, "ETH", tx.Asset)
	assert.Equal(t, -1, tx.LogIndex, "native transfers have no log index")
	assert.Nil(t, tx.Token, "native transfers should not populate Token")

	expectedAmount, _ := new(big.Int).SetString("14d1120d7b160000", 16)
	assert.Equal(t, 0, tx.Amount.Cmp(expectedAmount), "amount should match rawValue hex")
}

// ---------------------------------------------------------------------------
// ParsePayload — ERC-20 token transfer (category: token)
// ---------------------------------------------------------------------------

func TestParsePayload_ERC20TokenTransfer(t *testing.T) {
	payload := []byte(`{
		"event": {
			"activity": [{
				"blockNum": "0xabc",
				"hash": "0xtokentx789",
				"fromAddress": "0xTokenSender",
				"toAddress": "0xTokenReceiver",
				"value": 100.0,
				"asset": "USDC",
				"category": "token",
				"rawContract": {
					"rawValue": "0x5f5e100",
					"address": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
					"decimals": 6
				},
				"log": {
					"logIndex": "0x1a",
					"blockHash": "0xtokenblock456"
				}
			}]
		}
	}`)

	provider := NewAlchemyProvider("test-key")
	transfers, err := provider.ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)

	tx := transfers[0]
	assert.Equal(t, "0xtokentx789", tx.TxHash)
	assert.Equal(t, uint64(0xabc), tx.BlockNumber)
	assert.Equal(t, "0xtokenblock456", tx.BlockHash)
	assert.Equal(t, "0xTokenSender", tx.From)
	assert.Equal(t, "0xTokenReceiver", tx.To)
	assert.Equal(t, "USDC", tx.Asset)
	assert.Equal(t, 0x1a, tx.LogIndex, "logIndex should be parsed from hex")

	expectedAmount, _ := new(big.Int).SetString("5f5e100", 16)
	assert.Equal(t, 0, tx.Amount.Cmp(expectedAmount))
	assert.False(t, tx.AmountIsHuman)

	require.NotNil(t, tx.Token)
	assert.Equal(t, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", tx.Token.Contract)
	assert.Equal(t, uint8(6), tx.Token.Decimals)
}

// ---------------------------------------------------------------------------
// diffAddresses
// ---------------------------------------------------------------------------

func TestParsePayload_ERC20OmitsRawValueKeepsHumanAmount(t *testing.T) {
	payload := []byte(`{
		"event": {
			"activity": [{
				"blockNum": "0x1",
				"hash": "0xhuman",
				"fromAddress": "0xfrom",
				"toAddress": "0xto",
				"value": 1.5,
				"asset": "USDT",
				"category": "token",
				"rawContract": {
					"rawValue": "",
					"address": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
					"decimals": 18
				}
			}]
		}
	}`)

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	tx := transfers[0]
	assert.True(t, tx.AmountIsHuman)
	assert.Equal(t, "1.5", tx.HumanAmount)
	scaled, _ := new(big.Int).SetString("1500000000000000000", 10)
	if tx.Amount != nil && tx.Amount.Cmp(scaled) == 0 {
		t.Fatal("parser must not scale the human token amount by 18")
	}
}

func TestDiffAddresses(t *testing.T) {
	current := []string{"0xAAA", "0xBBB", "0xCCC"}
	desired := []string{"0xBBB", "0xDDD"}

	toAdd, toRemove := diffAddresses(current, desired)
	assert.Equal(t, []string{"0xDDD"}, toAdd)
	assert.Equal(t, []string{"0xAAA", "0xCCC"}, toRemove)
}

func TestDiffAddresses_CaseInsensitive(t *testing.T) {
	current := []string{"0xaaa"}
	desired := []string{"0xAAA"}

	toAdd, toRemove := diffAddresses(current, desired)
	assert.Empty(t, toAdd, "case-insensitive match should not add")
	assert.Empty(t, toRemove, "case-insensitive match should not remove")
}

func TestDiffAddresses_NoChanges(t *testing.T) {
	addrs := []string{"0xAAA", "0xBBB"}

	toAdd, toRemove := diffAddresses(addrs, addrs)
	assert.Empty(t, toAdd)
	assert.Empty(t, toRemove)
}

func TestAlchemyHeaders_ReadTheKeySourceOnEveryCall(t *testing.T) {
	const (
		opened = "ing-opened-a91c"
		env    = "ing-env-44d0"
		boot   = "ing-boot-must-not-stick"
	)
	logs := captureAlchemyLogs(t)
	n := 0
	provider := NewAlchemyProvider(boot).UseKeySource(func(context.Context) string {
		n++
		if n == 1 {
			return opened
		}
		return env
	})

	first, err := provider.apiHeaders(context.Background())
	if err != nil || first[alchemyAuthTokenHdr] != opened {
		t.Fatal("the first call did not use the key source")
	}
	second, err := provider.apiHeaders(context.Background())
	if err != nil || second[alchemyAuthTokenHdr] != env || n != 2 {
		t.Fatal("the second call reused the first key")
	}
	requireAlchemyLogsOmit(t, logs.String(), opened, env, boot, "enc:v1:")
}

func captureAlchemyLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func requireAlchemyLogsOmit(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatal("a credential appeared in a log line")
		}
	}
}
