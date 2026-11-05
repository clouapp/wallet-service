package ramps_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func bankOptions(t *testing.T, h *bankHarness) map[string]ramps.BankRailOption {
	t.Helper()
	view, err := h.offramp.BankOptions(context.Background(), user, person)
	require.NoError(t, err)
	require.Len(t, view.Rails, 2)
	byRail := map[string]ramps.BankRailOption{}
	for _, option := range view.Rails {
		byRail[option.Rail] = option
	}
	return byRail
}

func TestBankOptionsListsBothRailsClosedWhileTheFlagIs(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.flagOn = false

	for rail, option := range bankOptions(t, h) {
		require.False(t, option.Available, rail)
		require.Equal(t, "flag_closed", option.Reason, rail)
		require.False(t, option.RequiresSolForFee, rail)
	}
}

func TestBankOptionsBoundTheAmountByTheRailAndTheAllowance(t *testing.T) {
	for name, tc := range map[string]struct {
		minUSD, maxUSD, left string
		wantMin, wantMax     string
	}{
		"an unbounded rail takes the app floor and the allowance": {"", "", "1000.00", "5.00", "1000.00"},
		"the rail's own minimum wins above the floor":             {"10", "", "1000.00", "10.00", "1000.00"},
		"the floor wins above a smaller rail minimum":             {"1", "", "1000.00", "5.00", "1000.00"},
		"the rail's maximum wins above the allowance":             {"", "500", "1000.00", "5.00", "500.00"},
		"the allowance wins above the rail's maximum":             {"", "500", "300.00", "5.00", "300.00"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBankHarness(t, ramp.RailACHPayout, true)
			h.minUSD, h.maxUSD = tc.minUSD, tc.maxUSD
			h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).Return(tc.left, nil)

			for rail, option := range bankOptions(t, h) {
				require.True(t, option.Available, rail)
				require.Empty(t, option.Reason, rail)
				require.Equal(t, []string{"USDC", "USDT"}, option.Tokens, rail)
				require.Equal(t, "solana", option.Chain, rail)
				require.Equal(t, tc.wantMin, option.MinUSD, rail)
				require.Equal(t, tc.wantMax, option.MaxUSD, rail)
				require.True(t, option.RequiresSolForFee, rail)
			}
		})
	}
}

// usdtOnlyAdapter is a BlindPay instance with only the USDT mint configured:
// it serves a capability naming no token ("any ready token") or USDT.
type usdtOnlyAdapter struct {
	*mocks.MockProviderAdapter
}

func (a usdtOnlyAdapter) Supports(c ramp.Capability) bool {
	return c.Token == "" || c.Token == models.TargetTokenUSDT
}

func TestBankOptionsAreOpenWithOnlyTheUSDTMint(t *testing.T) {
	h := buildBankHarness(t, ramp.RailACHPayout, func(h *bankHarness) ramp.ProviderAdapter {
		return usdtOnlyAdapter{MockProviderAdapter: h.blindpay}
	})

	for rail, option := range bankOptions(t, h) {
		require.True(t, option.Available, rail)
		require.Empty(t, option.Reason, rail)
		require.Equal(t, []string{"USDT"}, option.Tokens, rail)
	}
}

func TestBankOptionsWithoutAnAllowanceReaderTakeTheRailMaximum(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, false)
	h.maxUSD = "750"

	for rail, option := range bankOptions(t, h) {
		require.True(t, option.Available, rail)
		require.Equal(t, "750.00", option.MaxUSD, rail)
	}
}

func TestBankOptionsWithNoBoundAtAllLeaveTheMaximumEmpty(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, false)

	for rail, option := range bankOptions(t, h) {
		require.True(t, option.Available, rail)
		require.Empty(t, option.MaxUSD, rail)
	}
}

// The options screen must not fall because the provider's allowance could not
// be read: the rail is unavailable, not the request failed.
func TestBankOptionsAnAllowanceThatCannotBeReadMakesTheRailUnavailable(t *testing.T) {
	h := newBankHarness(t, ramp.RailACHPayout, true)
	h.limits.EXPECT().RemainingPayoutUSD(testifymock.Anything, testifymock.Anything).
		Return("", errors.Join(ramp.ErrProviderFailed, errors.New("boom")))

	for rail, option := range bankOptions(t, h) {
		require.False(t, option.Available, rail)
		require.Equal(t, "provider_unavailable", option.Reason, rail)
		require.Empty(t, option.MaxUSD, rail)
		require.False(t, option.RequiresSolForFee, rail)
	}
}
