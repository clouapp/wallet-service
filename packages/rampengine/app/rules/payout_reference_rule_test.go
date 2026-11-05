package rules

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Bridge's limits: an ACH reference is at most 10 letters, digits or spaces;
// a wire message at most 140 printable characters (a line break counts) that
// fit Fedwire's 4 lines of 35. The sandbox (2026-10-15) refused 141 and
// "35\n35\n35\n35" (143) but took 5 short lines; the doc says the message is
// validated against 4 × 35, so we hold it here instead of trusting upstream.
func TestValidPayoutReference(t *testing.T) {
	for _, tc := range []struct {
		name, rail, reference string
		want                  bool
	}{
		{"none on ach", models.RailUSDACHPayout, "", true},
		{"none on wire", models.RailUSDWirePayout, "", true},
		{"ach letters, digits and spaces", models.RailUSDACHPayout, "XIP 001", true},
		{"ach ten characters", models.RailUSDACHPayout, "ABCDE12345", true},
		{"ach eleven characters", models.RailUSDACHPayout, "ABCDE123456", false},
		{"ach punctuation", models.RailUSDACHPayout, "XIP-001", false},
		{"ach accent", models.RailUSDACHPayout, "Aluguél", false},
		{"wire printable", models.RailUSDWirePayout, "Rent October #10, unit 4B", true},
		{"wire 140 characters", models.RailUSDWirePayout, strings.Repeat("a", 140), true},
		{"wire 141 characters", models.RailUSDWirePayout, strings.Repeat("a", 141), false},
		{"wire four lines of 35 joined (143)", models.RailUSDWirePayout, strings.Repeat(strings.Repeat("a", 35)+"\n", 3) + strings.Repeat("a", 35), false},
		{"wire five short lines", models.RailUSDWirePayout, "a\nb\nc\nd\ne", false},
		{"wire four short lines", models.RailUSDWirePayout, "a\nb\nc\nd", true},
		{"wire a 36 line wraps to 2, plus 1", models.RailUSDWirePayout, strings.Repeat("a", 36) + "\nx", true},
		{"wire 106 then a line wraps to 5", models.RailUSDWirePayout, strings.Repeat("a", 106) + "\nx", false},
		{"wire carriage return", models.RailUSDWirePayout, "Rent\r\nOctober", false},
		{"wire empty line", models.RailUSDWirePayout, "Rent\n\nOctober", true},
		{"a pix rail", models.RailPixPayout, "XIP 001", false},
		{"no rail", "", "XIP 001", false},
	} {
		require.Equal(t, tc.want, ValidPayoutReference(tc.rail, tc.reference), tc.name)
	}
}

// The limit is the rail's: the rule reads it off the request.
func TestPayoutReferenceRuleReadsTheRailOfTheRequest(t *testing.T) {
	rule := &PayoutReferenceRule{}
	ctx := context.Background()
	require.Equal(t, "payout_reference", rule.Signature())
	require.True(t, rule.Passes(ctx, mapData{"rail": models.RailUSDWirePayout}, "Rent October, unit 4B"))
	require.False(t, rule.Passes(ctx, mapData{"rail": models.RailUSDACHPayout}, "Rent October, unit 4B"))
	require.True(t, rule.Passes(ctx, mapData{"rail": models.RailUSDACHPayout}, nil), "an absent reference is none")
	require.False(t, rule.Passes(ctx, mapData{"rail": models.RailUSDACHPayout}, 12))
}
