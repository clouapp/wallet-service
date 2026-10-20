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
	"github.com/macrowallets/waas/pkg/types"

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

func TestVerify_Inbound_ValidSignature(t *testing.T) {
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

func TestVerify_Inbound_InvalidSignature(t *testing.T) {
	provider := NewAlchemyProvider("test-key")
	body := []byte(`{"event":"test"}`)
	secret := "whsec_test_secret"

	headers := providers.Header{}
	headers.Set("X-Alchemy-Signature", "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678")

	valid, err := provider.VerifyInbound(headers, body, secret)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestVerify_Inbound_MissingHeader(t *testing.T) {
	provider := NewAlchemyProvider("test-key")
	body := []byte(`{"event":"test"}`)

	headers := providers.Header{}

	valid, err := provider.VerifyInbound(headers, body, "some-secret")
	require.Error(t, err)
	assert.False(t, valid)
	assert.Contains(t, err.Error(), "missing")
}

// ---------------------------------------------------------------------------
// ParsePayload — native ETH transfer (category: external)
// ---------------------------------------------------------------------------

func TestParse_Payload_NativeETHTransfer(t *testing.T) {
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

func TestParse_Payload_ERC20TokenTransfer(t *testing.T) {
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

func TestParse_Payload_NativeOmitsRawValueKeepsHumanAmount(t *testing.T) {
	payload := []byte(`{
		"event": {
			"activity": [{
				"blockNum": "0x1",
				"hash": "0xnativehuman",
				"fromAddress": "0xfrom",
				"toAddress": "0xto",
				"value": 1.5,
				"asset": "ETH",
				"category": "external",
				"rawContract": {"rawValue": "", "address": "", "decimals": 18}
			}]
		}
	}`)

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	tx := transfers[0]
	assert.True(t, tx.AmountIsHuman)
	assert.Equal(t, "1.5", tx.HumanAmount)
	assert.Nil(t, tx.Amount)
	assert.Nil(t, tx.Token)
}

func TestParse_Payload_ERC20OmitsRawValueKeepsHumanAmount(t *testing.T) {
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
				},
				"log": {"logIndex": "0x0", "blockHash": "0xblock"}
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

// activityPayload is one ADDRESS_ACTIVITY webhook holding activities.
func activityPayload(activities ...string) []byte {
	body := `{"event":{"activity":[`
	for i, activity := range activities {
		if i > 0 {
			body += ","
		}
		body += activity
	}
	return []byte(body + `]}}`)
}

const usdcActivityAtLogZero = `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xfrom","toAddress":"0xto","value":1,"asset":"USDC","category":"token",` +
	`"rawContract":{"rawValue":"0xf4240","address":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","decimals":6},"log":{"logIndex":"0x0","blockHash":"0xblock"}}`

func TestParse_Payload_TheFirstLogOfABlockIsPositionZero(t *testing.T) {
	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(usdcActivityAtLogZero))
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	assert.Equal(t, 0, transfers[0].LogIndex)
}

func TestParse_Payload_SkipsATokenTransferWithoutItsLogIndexAndKeepsTheOthers(t *testing.T) {
	withoutLog := `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xfrom","toAddress":"0xto","value":1,"asset":"USDC","category":"token",` +
		`"rawContract":{"rawValue":"0xf4240","address":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","decimals":6}}`

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(withoutLog, usdcActivityAtLogZero))
	require.NoError(t, err)
	require.Len(t, transfers, 1, "a token transfer at the native value position would hide the transaction's native deposit")
	assert.Equal(t, 0, transfers[0].LogIndex)
}

func TestParse_Payload_RefusesABodyThatIsNotAnEvent(t *testing.T) {
	_, err := NewAlchemyProvider("test-key").ParsePayload([]byte(`[1,2]`))
	assert.Error(t, err)
}

func internalActivity(to, rawValue, traceAddress string) string {
	trace := ""
	if traceAddress != "" {
		trace = `,"typeTraceAddress":"` + traceAddress + `"`
	}
	return `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xdisperse","toAddress":"` + to + `","value":0.5,"asset":"ETH","category":"internal",` +
		`"rawContract":{"rawValue":"` + rawValue + `","address":"","decimals":18}` + trace + `}`
}

func TestParse_Payload_GivesEachInternalTransferAPositionOfItsOwn(t *testing.T) {
	payload := activityPayload(
		internalActivity("0xalice", "0x6f05b59d3b20000", "call_0_1"),
		internalActivity("0xbob", "0x6f05b59d3b20000", "call_0_2"),
		`{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xpayer","toAddress":"0xcarol","value":1,"asset":"ETH","category":"external","rawContract":{"rawValue":"0xde0b6b3a7640000","address":"","decimals":18}}`,
	)

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(payload)
	require.NoError(t, err)
	require.Len(t, transfers, 3)

	alice, bob, carol := transfers[0].LogIndex, transfers[1].LogIndex, transfers[2].LogIndex
	assert.Equal(t, types.NativeValuePosition, carol, "the transaction's own value")
	assert.Less(t, alice, types.NativeValuePosition, "below the native value position, which the scanner reports")
	assert.Less(t, bob, types.NativeValuePosition)
	assert.NotEqual(t, alice, bob, "a batch payout pays each address its own deposit")
	assert.Nil(t, transfers[0].Token)

	again, err := NewAlchemyProvider("test-key").ParsePayload(payload)
	require.NoError(t, err)
	assert.Equal(t, alice, again[0].LogIndex, "a redelivery numbers it alike")
	assert.Equal(t, -2-int(fnv32a("call_0_1")%(1<<30)), alice)
}

func TestParse_Payload_SkipsAnInternalTransferWithoutItsTraceAddress(t *testing.T) {
	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(internalActivity("0xalice", "0x1", ""), usdcActivityAtLogZero))
	require.NoError(t, err)
	require.Len(t, transfers, 1, "without a trace address it would take another transfer's position")
	assert.Equal(t, "USDC", transfers[0].Asset)
}

func TestParse_Payload_SkipsATransferARemovedLogCarries(t *testing.T) {
	removed := `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xfrom","toAddress":"0xto","value":1,"asset":"USDC","category":"token",` +
		`"rawContract":{"rawValue":"0xf4240","address":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","decimals":6},"log":{"logIndex":"0x1","blockHash":"0xorphaned","removed":true}}`

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(removed, usdcActivityAtLogZero))
	require.NoError(t, err)
	require.Len(t, transfers, 1, "a log of a block reorganized away moved nothing")
	assert.Equal(t, 0, transfers[0].LogIndex)
}

func TestParse_Payload_ReadsTheERC20CategoryAsAToken(t *testing.T) {
	erc20 := strings.Replace(usdcActivityAtLogZero, `"category":"token"`, `"category":"erc20"`, 1)

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(erc20))
	require.NoError(t, err)
	require.Len(t, transfers, 1)
	require.NotNil(t, transfers[0].Token)
	assert.Equal(t, 0, transfers[0].LogIndex)
}

func TestParse_Payload_LeavesOutNFTTransfers(t *testing.T) {
	erc721 := `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xfrom","toAddress":"0xto","value":null,"asset":"PUNK","category":"erc721",` +
		`"erc721TokenId":"0x1","rawContract":{"rawValue":null,"address":"0xb47e3cd837dDF8e4c57F05d70Ab865de6e193BBB"},"log":{"logIndex":"0x2","blockHash":"0xblock"}}`
	erc1155 := `{"blockNum":"0x10","hash":"0xbatch","fromAddress":"0xfrom","toAddress":"0xto","value":null,"asset":null,"category":"erc1155",` +
		`"rawContract":{"rawValue":null,"address":"0x76BE3b62873462d2142405439777e971754E8E77"},"log":{"logIndex":"0x3","blockHash":"0xblock"}}`

	transfers, err := NewAlchemyProvider("test-key").ParsePayload(activityPayload(erc721, usdcActivityAtLogZero, erc1155))
	require.NoError(t, err)
	require.Len(t, transfers, 1, "an NFT is not a deposit and must not take the native value position")
	assert.Equal(t, "USDC", transfers[0].Asset)
}

func TestClient_Diff_Addresses(t *testing.T) {
	current := []string{"0xAAA", "0xBBB", "0xCCC"}
	desired := []string{"0xBBB", "0xDDD"}

	toAdd, toRemove := diffAddresses(current, desired)
	assert.Equal(t, []string{"0xDDD"}, toAdd)
	assert.Equal(t, []string{"0xAAA", "0xCCC"}, toRemove)
}

func TestDiff_Addresses_CaseInsensitive(t *testing.T) {
	current := []string{"0xaaa"}
	desired := []string{"0xAAA"}

	toAdd, toRemove := diffAddresses(current, desired)
	assert.Empty(t, toAdd, "case-insensitive match should not add")
	assert.Empty(t, toRemove, "case-insensitive match should not remove")
}

func TestDiff_Addresses_NoChanges(t *testing.T) {
	addrs := []string{"0xAAA", "0xBBB"}

	toAdd, toRemove := diffAddresses(addrs, addrs)
	assert.Empty(t, toAdd)
	assert.Empty(t, toRemove)
}

func TestAlchemy_Headers_ReadTheKeySourceOnEveryCall(t *testing.T) {
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
