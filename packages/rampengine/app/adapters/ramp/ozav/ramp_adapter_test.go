package ozav

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// Ozav filtered no token at all: anything the client typed was forwarded and
// left to come back a 4xx. The vocabulary is a property of what this adapter
// knows how to quote and settle, so it is declared here and refused locally.
func TestSupportsRefusesATokenOzavCannotPrice(t *testing.T) {
	a := New(nil, nil)
	usdc := ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "solana"}
	require.True(t, a.Supports(usdc))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDT", Chain: "solana"}))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "BRS", Chain: "solana"}))
	require.False(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "SOL", Chain: "solana"}),
		"a token it cannot price is a quote the user could never redeem")
}

func TestSupportsMatchesTheTokenWhateverCaseItArrivesIn(t *testing.T) {
	a := New(nil, nil)
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "usdc"}))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "UsDt"}))
}

// An unnamed token or chain is a question the caller did not ask, and the
// sticky create path asks none: it resolves on the rail alone.
func TestSupportsAcceptsACapabilityThatNamesNoTokenOrChain(t *testing.T) {
	a := New(nil, nil)
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout}))
	require.True(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayin, Token: "USDC"}))
	require.False(t, a.Supports(ramp.Capability{Rail: ramp.RailPixPayout, Token: "USDC", Chain: "polygon"}))
}

// Which collection mode Ozav prefers is one line, and it is a decision the
// design spec argued at length rather than an incidental value — so it is
// pinned. If it changes, it should change because someone measured a real
// production run, not because a refactor moved it.
//
// The production run happened on 03/09/2026 and confirmed it: Ozav verifies an
// individual from the customer record and the documents uploaded against it,
// which is exactly what api_docs means. The alternative this comment used to
// hold open — switching to hosted verification — is the thing that freezes the
// customer. See verification_test.go.
func TestPreferredKycModeIsApiDocs(t *testing.T) {
	require.Equal(t, ramp.KycModeAPIDocs, (&Adapter{}).PreferredKycMode())
}
