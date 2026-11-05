package blindpay_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
)

var _ ramp.ProviderAdapter = (*blindpay.Adapter)(nil)

// The adapter is implemented for KYC and, from E13, for Pix payins of USDC/USDT
// on Solana. Every other rail stays closed until its stage.
func TestAdapter_IsImplementedForKycAndPixPayin(t *testing.T) {
	adapter := blindpay.New(nil, nil, nil)
	assert.Equal(t, ramp.ProviderBlindPay, adapter.Code())
	assert.Equal(t, ramp.KycModeAPIDocs, adapter.PreferredKycMode(), "BlindPay collects documents through its API")
	assert.True(t, adapter.RequiresTos(), "BlindPay has terms of its own to accept before a customer exists")

	factory := ramp.NewFactory(adapter)
	assert.True(t, factory.IsImplemented(ramp.ProviderBlindPay))
	got, err := factory.Get(ramp.ProviderBlindPay)
	assert.NoError(t, err)
	assert.Same(t, adapter, got)

	for _, token := range []string{"USDC", "usdt", "USDT"} {
		for _, chain := range []string{"solana", ""} {
			assert.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: token, Chain: chain}), "pix_payin %s on %q", token, chain)
		}
	}
	assert.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC", Chain: "Solana"}))
	assert.False(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC", Chain: "ethereum"}))
	assert.False(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "BRLA", Chain: "solana"}))
	assert.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayin}))
	for _, rail := range []ramp.Rail{ramp.RailPixPayout, ramp.RailACHPayout, ramp.RailWirePayout} {
		for _, token := range []string{"", "USDC", "USDT"} {
			assert.False(t, adapter.Supports(ramp.Capability{Rail: rail, Token: token, Chain: "solana"}), "rail %s token %q", rail, token)
		}
	}
}

// Every operation not yet built refuses before anything is sent.
func TestAdapter_RefusesEveryOperationNotYetBuilt(t *testing.T) {
	ctx, ref := context.Background(), ramp.CustomerRef{ExternalID: "cu_1"}
	adapter := blindpay.New(nil, nil, nil)

	_, err := adapter.CreateOfframp(ctx, ref, ramp.OfframpCreateRequest{})
	assert.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
	_, err = adapter.CreateHostedKycLink(ctx, ref, ramp.HostedLinkOpts{})
	assert.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

// Every rail, token and chain against every gate. The bank rails open only
// for USDC/USDT on Solana (or unnamed) and only when the gate agrees; an
// unnamed token asks "any token".
func TestAdapter_SupportsMatrix(t *testing.T) {
	gates := map[string]blindpay.PayoutGate{
		"true":     func(string) bool { return true },
		"false":    func(string) bool { return false },
		"nil":      nil,
		"usdcOnly": func(token string) bool { return token == "USDC" },
	}
	rails := []ramp.Rail{ramp.RailACHPayout, ramp.RailWirePayout, ramp.RailPixPayin, ramp.RailPixPayout}
	tokens := []string{"", "USDC", "USDT", "BRLA"}
	chains := []string{"", "solana", "ethereum"}

	for gateName, gate := range gates {
		for _, rail := range rails {
			for _, token := range tokens {
				for _, chain := range chains {
					want := false
					payout := rail == ramp.RailACHPayout || rail == ramp.RailWirePayout || rail == ramp.RailPixPayout
					shapeOK := (token == "" || token == "USDC" || token == "USDT") && (chain == "" || chain == "solana")
					switch {
					case rail == ramp.RailPixPayin:
						want = shapeOK
					case payout && shapeOK:
						switch gateName {
						case "true":
							want = true
						case "usdcOnly":
							want = token == "" || token == "USDC"
						}
					}
					got := blindpay.New(nil, nil, gate).Supports(ramp.Capability{Rail: rail, Token: token, Chain: chain})
					assert.Equal(t, want, got, "gate %s rail %s token %q chain %q", gateName, rail, token, chain)
				}
			}
		}
	}
}
