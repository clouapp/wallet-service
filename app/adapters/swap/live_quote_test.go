package swapproviders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/stretchr/testify/require"
)

// This opt-in check calls the official API with optional authentication and an
// explicitly supplied public address. It never loads custody or broadcasts.
func TestLiveLiFi_ReadOnlyQuoteAndBuild(t *testing.T) {
	if os.Getenv("SWAP_LIVE_QUOTE_CHECK") != "1" {
		t.Skip("opt-in read-only official provider check")
	}
	address := func(label string) string {
		hash := sha256.Sum256([]byte(label))
		return "0x" + hex.EncodeToString(hash[:20])
	}
	key := strings.TrimSpace(os.Getenv("LIFI_API_KEY"))
	source := strings.TrimSpace(os.Getenv("LIFI_LIVE_SOURCE_ADDRESS"))
	destination := address("macro-wallets-read-only-probe-to")
	addressMode := "synthetic"
	if source == "" {
		source = address("macro-wallets-read-only-probe-from")
	} else {
		destination = source
		addressMode = "supplied public wallet"
	}
	inputRaw := strings.TrimSpace(os.Getenv("LIFI_LIVE_AMOUNT_RAW"))
	if inputRaw == "" {
		inputRaw = "1000000000000000"
	}
	request := swap.QuoteRequest{
		From:        swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true},
		To:          swap.Asset{Symbol: "USDC", Chain: "eth", NetworkID: 1, Decimals: 6, Contract: models.USDCContractETH},
		FromAddress: source,
		ToAddress:   destination,
		AmountRaw:   inputRaw, SlippageBPS: 100,
	}
	require.NoError(t, request.Validate())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapter := NewLiFi(Deps{})
	credential := swap.ProviderConfig{Enabled: true, APIKey: key}
	quote, err := adapter.Quote(ctx, request, credential)
	require.NoError(t, err, "the official provider must return a valid live quote")
	require.NoError(t, quote.Validate(time.Now()))
	require.Equal(t, swap.ProviderLiFi, quote.Provider)
	funding, err := adapter.Build(ctx, quote, credential)
	require.NoError(t, err)
	require.Equal(t, "evm", funding.Kind)
	require.Equal(t, int64(1), funding.ChainID)
	base, value := 10, funding.AmountRaw
	if strings.HasPrefix(value, "0x") {
		base, value = 16, strings.TrimPrefix(value, "0x")
	}
	units, ok := new(big.Int).SetString(value, base)
	require.True(t, ok, "native value must be an integer")
	require.Equal(t, request.AmountRaw, units.String(), "fund exactly the quoted native input")
	require.NotEmpty(t, funding.Data)
	t.Logf("read-only live provider=%s route=%s input_raw=%s expected_raw=%s minimum_raw=%s expires=%s authenticated=%t address_mode=%s; no signing or broadcast", quote.Provider, quote.RouteID, request.AmountRaw, quote.ExpectedRaw, quote.MinimumRaw, quote.ExpiresAt.UTC().Format(time.RFC3339), key != "", addressMode)
}

// The authenticated check only requests a quote and unsigned transaction data.
// Supply the temporary key through the process environment, never source files.
func TestLiveSwapKit_ReadOnlyQuoteAndBuild(t *testing.T) {
	if os.Getenv("SWAPKIT_LIVE_QUOTE_CHECK") != "1" {
		t.Skip("opt-in authenticated read-only official provider check")
	}
	key := strings.TrimSpace(os.Getenv("SWAPKIT_API_KEY"))
	require.NotEmpty(t, key, "supply SWAPKIT_API_KEY through the process environment")
	address := func(label string) string {
		hash := sha256.Sum256([]byte(label))
		return "0x" + hex.EncodeToString(hash[:20])
	}
	source := strings.TrimSpace(os.Getenv("SWAPKIT_LIVE_SOURCE_ADDRESS"))
	hasSuppliedSource := source != ""
	destination := address("macro-wallets-read-only-probe-to")
	addressMode := "synthetic"
	if hasSuppliedSource {
		destination = source
		addressMode = "supplied public wallet"
	} else {
		source = address("macro-wallets-read-only-probe-from")
	}
	request := swap.QuoteRequest{
		From:        swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true},
		To:          swap.Asset{Symbol: "USDC", Chain: "eth", NetworkID: 1, Decimals: 6, Contract: models.USDCContractETH},
		FromAddress: source,
		ToAddress:   destination,
		AmountRaw:   "10000000000000000", SlippageBPS: 100,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapter := NewSwapKit(Deps{})
	credential := swap.ProviderConfig{Enabled: true, APIKey: key}
	quote, err := adapter.Quote(ctx, request, credential)
	require.NoError(t, err, "the official provider must return a valid authenticated quote")
	require.NoError(t, quote.Validate(time.Now()))
	require.Equal(t, swap.ProviderSwapKit, quote.Provider)
	t.Logf("read-only live provider=%s route=%s input_raw=%s expected_raw=%s minimum_raw=%s estimated_seconds=%d expires=%s address_mode=%s; no signing or broadcast", quote.Provider, quote.RouteID, request.AmountRaw, quote.ExpectedRaw, quote.MinimumRaw, quote.EstimatedSeconds, quote.ExpiresAt.UTC().Format(time.RFC3339), addressMode)
	t.Run("unsigned_build", func(t *testing.T) {
		if !hasSuppliedSource {
			t.Skip("building requires an explicitly supplied funded SWAPKIT_LIVE_SOURCE_ADDRESS; synthetic addresses cannot pass the provider balance check")
		}
		funding, err := adapter.Build(ctx, quote, credential)
		require.NoError(t, err, "the official provider must return unsigned funding data")
		require.Equal(t, "evm", funding.Kind)
		require.Equal(t, int64(1), funding.ChainID)
		base, value := 10, funding.AmountRaw
		if strings.HasPrefix(value, "0x") {
			base, value = 16, strings.TrimPrefix(value, "0x")
		}
		units, ok := new(big.Int).SetString(value, base)
		require.True(t, ok, "native value must be an integer")
		require.Equal(t, request.AmountRaw, units.String(), "fund exactly the quoted native input")
		require.NotEmpty(t, funding.Data)
	})
}
