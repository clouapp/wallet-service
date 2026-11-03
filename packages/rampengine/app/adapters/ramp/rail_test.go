package ramp_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/featureflags/catalog"
	rampflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
)

func TestRail_Flag(t *testing.T) {
	assert.Equal(t, rampflags.PayinEnabled, ramp.RailPixPayin.Flag())
	assert.Equal(t, rampflags.PayoutEnabled, ramp.RailPixPayout.Flag())
}

// A rail nobody gave a switch is closed, not open: reading the flag it names
// fails, and every caller refuses on a failed read.
func TestRail_Flag_ARailWithoutOneReadsAsAFailure(t *testing.T) {
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)

	on, err := m.IsActive(context.Background(), ramp.Rail("sepa_payin").Flag(), models.UserFlagScope(1))
	assert.Error(t, err)
	assert.False(t, on)
}

func TestRail_Flag_BankRailsShareTheBankSwitch(t *testing.T) {
	assert.Equal(t, rampflags.BankPayoutEnabled, ramp.RailACHPayout.Flag())
	assert.Equal(t, rampflags.BankPayoutEnabled, ramp.RailWirePayout.Flag())
}

func TestBankRails(t *testing.T) {
	assert.Equal(t, []ramp.Rail{ramp.RailACHPayout, ramp.RailWirePayout}, ramp.BankRails())
}

func TestRail_IsBank(t *testing.T) {
	assert.True(t, ramp.RailACHPayout.IsBank())
	assert.True(t, ramp.RailWirePayout.IsBank())
	assert.False(t, ramp.RailPixPayin.IsBank())
	assert.False(t, ramp.RailPixPayout.IsBank())
	assert.False(t, ramp.Rail("sepa_payout").IsBank(), "an unknown rail is not a bank rail")
}

// The bank switch is closed until someone opens it, and its global value is a
// veto over the per-user one. The beta rollout depends on exactly this:
// global on plus a per-user override reaches only the overridden users, and
// turning the global off closes the rail for everyone again.
func TestRail_Flag_BankSwitchRollout(t *testing.T) {
	ctx := context.Background()
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	flag := ramp.RailACHPayout.Flag()
	beta, other := models.UserFlagScope(1), models.UserFlagScope(2)

	on, err := m.IsActive(ctx, flag, beta)
	require.NoError(t, err)
	assert.False(t, on, "closed until someone decides to open it")

	require.NoError(t, m.Activate(ctx, flag, beta))
	on, err = m.IsActive(ctx, flag, beta)
	require.NoError(t, err)
	assert.False(t, on, "a user override alone must not pass the global veto")

	require.NoError(t, m.Activate(ctx, flag, nil))
	on, err = m.IsActive(ctx, flag, beta)
	require.NoError(t, err)
	assert.True(t, on, "global on plus the override opens it for the beta user")
	on, err = m.IsActive(ctx, flag, other)
	require.NoError(t, err)
	assert.False(t, on, "a user without the override still reads the closed default")

	require.NoError(t, m.Deactivate(ctx, flag, nil))
	on, err = m.IsActive(ctx, flag, beta)
	require.NoError(t, err)
	assert.False(t, on, "the global off is the kill switch")
}

// IsBank follows the rail list: a rail added to BankRails is a bank rail
// everywhere that branches on it.
func TestRail_IsBankAgreesWithBankRails(t *testing.T) {
	for _, rail := range ramp.BankRails() {
		assert.True(t, rail.IsBank(), "%s", rail)
	}
	for _, rail := range ramp.PixRails() {
		assert.False(t, rail.IsBank(), "%s", rail)
	}
}

// One switch closes the USD deposit; one closes both USD payouts.
func TestRail_Flag_USD(t *testing.T) {
	assert.Equal(t, rampflags.USDPayinEnabled, ramp.RailUSDPayin.Flag())
	assert.Equal(t, rampflags.USDPayoutEnabled, ramp.RailUSDACHPayout.Flag())
	assert.Equal(t, rampflags.USDPayoutEnabled, ramp.RailUSDWirePayout.Flag())
}

func TestAllRails_IsPixThenUSD(t *testing.T) {
	assert.Equal(t, []ramp.Rail{ramp.RailPixPayin, ramp.RailPixPayout,
		ramp.RailUSDPayin, ramp.RailUSDACHPayout, ramp.RailUSDWirePayout}, ramp.AllRails())
	assert.Equal(t, []ramp.Rail{ramp.RailUSDPayin, ramp.RailUSDACHPayout, ramp.RailUSDWirePayout}, ramp.USDRails())
}

func TestRail_FiatCurrencyAndIsUSDPayout(t *testing.T) {
	assert.Equal(t, "brl", ramp.RailPixPayout.FiatCurrency())
	assert.Equal(t, "usd", ramp.RailUSDPayin.FiatCurrency())
	assert.Equal(t, "", ramp.Rail("sepa_payin").FiatCurrency())

	assert.True(t, ramp.RailUSDACHPayout.IsUSDPayout())
	assert.True(t, ramp.RailUSDWirePayout.IsUSDPayout())
	assert.False(t, ramp.RailUSDPayin.IsUSDPayout())
	assert.False(t, ramp.RailPixPayout.IsUSDPayout())
}

func TestPayinRailFor(t *testing.T) {
	for currency, want := range map[string]ramp.Rail{
		"brl": ramp.RailPixPayin, "BRL": ramp.RailPixPayin,
		"usd": ramp.RailUSDPayin, " USD ": ramp.RailUSDPayin,
	} {
		got, ok := ramp.PayinRailFor(currency)
		assert.True(t, ok, currency)
		assert.Equal(t, want, got, currency)
	}
	_, ok := ramp.PayinRailFor("eur")
	assert.False(t, ok, "a currency with no rail is refused, never defaulted")
}

// Every rail flag is catalogued: reading one never fails on a fresh store.
func TestRail_Flag_EveryRailIsCatalogued(t *testing.T) {
	m := featureflag.NewManager(drivers.NewMemory())
	catalog.Register(m)
	for _, rail := range ramp.AllRails() {
		on, err := m.IsActive(context.Background(), rail.Flag(), models.UserFlagScope(1))
		assert.NoError(t, err, rail)
		assert.True(t, on, rail)
	}
}
