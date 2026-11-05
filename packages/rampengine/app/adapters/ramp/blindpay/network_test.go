package blindpay_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestLegFor(t *testing.T) {
	cases := []struct {
		mode, chain, token string
		want               blindpay.Leg
	}{
		// A development instance only takes BlindPay's test stablecoin, on
		// devnet; both tokens the product offers settle as USDB there.
		{"sandbox", "solana", "USDC", blindpay.Leg{Network: "solana_devnet", Token: "USDB"}},
		{"sandbox", "solana", "usdt", blindpay.Leg{Network: "solana_devnet", Token: "USDB"}},
		{"production", "solana", "USDC", blindpay.Leg{Network: "solana", Token: "USDC"}},
		{"production", "SOLANA", "usdt", blindpay.Leg{Network: "solana", Token: "USDT"}},
	}
	for _, c := range cases {
		leg, err := blindpay.LegFor(c.mode, c.chain, c.token)
		require.NoError(t, err, "%s %s %s", c.mode, c.chain, c.token)
		assert.Equal(t, c.want, leg)
	}
}

func TestLegFor_RefusesWhatBlindPayDoesNotSettle(t *testing.T) {
	for _, c := range [][3]string{
		{"production", "solana", "SOL"},
		{"production", "ethereum", "USDC"},
		{"production", "", "USDC"},
		{"staging", "solana", "USDC"},
		{"production", "solana", "USDB"},
	} {
		_, err := blindpay.LegFor(c[0], c[1], c[2])
		assert.ErrorIs(t, err, ramp.ErrNoProviderAvailable, "%v", c)
	}
}

// legAdapter is an adapter whose client runs in mode; PayoutLeg sends nothing.
func legAdapter(mode string) *blindpay.Adapter {
	client := blindpay.NewClient(blindpay.Config{APIKey: "bp_test_key", InstanceID: "in_000000000001", Mode: mode})
	return blindpay.New(func(context.Context) (*blindpay.Client, error) { return client, nil }, nil, nil)
}

// The binding is spelled the way the payout answers it, which is the
// instance's mode: devnet USDB on a development instance, the token itself
// on mainnet in production.
func TestPayoutLeg_IsSpelledInTheInstanceMode(t *testing.T) {
	cases := []struct {
		mode, chain, token     string
		wantNetwork, wantToken string
	}{
		{models.ProviderModeSandbox, "solana", "usdc", "solana_devnet", "USDB"},
		{models.ProviderModeProduction, "solana", "usdc", "solana", "USDC"},
		{models.ProviderModeProduction, "solana", "USDT", "solana", "USDT"},
	}
	for _, c := range cases {
		network, token, err := legAdapter(c.mode).PayoutLeg(context.Background(), c.chain, c.token)
		require.NoError(t, err, "%s %s %s", c.mode, c.chain, c.token)
		assert.Equal(t, c.wantNetwork, network)
		assert.Equal(t, c.wantToken, token)
	}
}

func TestPayoutLeg_RefusesWhatBlindPayDoesNotSettle(t *testing.T) {
	_, _, err := legAdapter(models.ProviderModeProduction).PayoutLeg(context.Background(), "ethereum", "usdc")
	assert.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

func TestPayoutLeg_WithoutAClientSendsNothing(t *testing.T) {
	adapter := blindpay.New(func(context.Context) (*blindpay.Client, error) { return nil, errors.New("no settings") }, nil, nil)
	_, _, err := adapter.PayoutLeg(context.Background(), "solana", "usdc")
	assert.ErrorIs(t, err, ramp.ErrRequestNotSent)
}
