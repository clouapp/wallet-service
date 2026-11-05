package ramp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func TestProviderGroupAndRailGroup(t *testing.T) {
	assert.Equal(t, "provider_bridge", ramp.ProviderGroup(ramp.ProviderBridge))
	assert.Equal(t, "rail_pix_payin", ramp.RailGroup(ramp.RailPixPayin))
}

func TestConfig_Provider(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return(models.ProviderModeSandbox, nil).Once()

	state, err := ramp.NewConfig(source).Provider(ramp.ProviderBridge)
	require.NoError(t, err)
	assert.True(t, state.IsUsable())
}

// A provider whose mode is neither of the two known values is not usable, even
// when it is enabled: the router must not route through a row it cannot read.
func TestConfig_Provider_UnknownModeIsNotUsable(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_ozav", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return("staging", nil).Once()

	state, err := ramp.NewConfig(source).Provider(ramp.ProviderOzav)
	require.NoError(t, err)
	assert.False(t, state.IsUsable())
}

// stubRailFees answers the per-provider fee keys a rail read now makes.
func stubRailFees(source *mocks.MockSettingsSource, group, bridgeFee, ozavFee string) {
	source.EXPECT().String(group, "fee_percent_blindpay").Return("0.00", nil).Once()
	source.EXPECT().String(group, "fee_percent_bridge").Return(bridgeFee, nil).Once()
	source.EXPECT().String(group, "fee_percent_ozav").Return(ozavFee, nil).Once()
}

func TestConfig_Rail(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("rail_pix_payin", "provider_order").Return("bridge, mutualpay", nil).Once()
	stubRailFees(source, "rail_pix_payin", "1.50", "0.80")
	source.EXPECT().String("rail_pix_payin", "min_amount_brl").Return("10.00", nil).Once()
	source.EXPECT().String("rail_pix_payin", "max_amount_brl").Return("", nil).Once()
	source.EXPECT().String("rail_pix_payin", "min_amount_brl_by_token").Return(`{"BRS":"10.00"}`, nil).Once()

	policy, err := ramp.NewConfig(source).Rail(ramp.RailPixPayin)
	require.NoError(t, err)
	assert.Equal(t, []string{"bridge", "mutualpay"}, policy.Providers)
	assert.Equal(t, map[ramp.ProviderCode]string{"bridge": "1.50", "ozav": "0.80", "blindpay": "0.00"}, policy.Fees)
	require.NotNil(t, policy.MinBRL)
	assert.Equal(t, "10.00", *policy.MinBRL)
	assert.Nil(t, policy.MaxBRL, "an empty limit means unbounded, not zero")
	assert.Nil(t, policy.MinUSD, "a pix rail limits in BRL only")
	assert.Nil(t, policy.MaxUSD)
	require.Equal(t, map[string]string{"BRS": "10.00"}, policy.MinBRLByToken)
}

// A hand-edited window that does not parse keeps the rail open on the
// default instead of failing every estimate on pix_payout.
func TestConfig_Rail_UnreadableFundingWindowFallsBackToTheDefault(t *testing.T) {
	for _, raw := range []string{"", "abc", "0", "-5"} {
		source := mocks.NewMockSettingsSource(t)
		source.EXPECT().String("rail_pix_payout", "provider_order").Return("ozav", nil).Once()
		stubRailFees(source, "rail_pix_payout", "0", "0")
		source.EXPECT().String("rail_pix_payout", "min_amount_brl").Return("", nil).Once()
		source.EXPECT().String("rail_pix_payout", "max_amount_brl").Return("", nil).Once()
		source.EXPECT().String("rail_pix_payout", "min_amount_brl_by_token").Return("", nil).Once()
		source.EXPECT().String("rail_pix_payout", "ozav_offramp_funding_window_minutes").Return(raw, nil).Once()
		source.EXPECT().String("rail_pix_payout", "ozav_deposit_match_tolerance").Return("", nil).Once()

		policy, err := ramp.NewConfig(source).Rail(ramp.RailPixPayout)
		require.NoError(t, err, raw)
		assert.Equal(t, ramp.DefaultOzavOfframpFundingWindowMinutes, policy.OzavOfframpFundingWindowMinutes, raw)
		assert.Equal(t, "0", policy.OzavDepositMatchTolerance, raw)
	}
}

func TestConfig_OzavDeliveryMaxAttempts(t *testing.T) {
	cases := map[string]int{"5": 5, "": ramp.DefaultOzavDeliveryMaxAttempts, "abc": ramp.DefaultOzavDeliveryMaxAttempts, "0": ramp.DefaultOzavDeliveryMaxAttempts}
	for raw, want := range cases {
		source := mocks.NewMockSettingsSource(t)
		source.EXPECT().String("rail_pix_payin", "ozav_delivery_max_attempts").Return(raw, nil).Once()
		assert.Equal(t, want, ramp.NewConfig(source).OzavDeliveryMaxAttempts(), raw)
	}
}

func TestRailPolicy_EffectiveMinBRLUsesPerTokenOverride(t *testing.T) {
	min := "400.00"
	policy := ramp.RailPolicy{
		MinBRL:        &min,
		MinBRLByToken: map[string]string{"BRS": "10.00"},
	}
	require.Equal(t, "10.00", *policy.EffectiveMinBRL("BRS"))
	require.Equal(t, "400.00", *policy.EffectiveMinBRL("USDC"))
}

// An empty order is what a rail closed for business looks like now that every
// rail always resolves to an effective value.
func TestConfig_Rail_EmptyOrderIsNotRoutable(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("rail_pix_payout", "provider_order").Return("", nil).Once()
	stubRailFees(source, "rail_pix_payout", "0.00", "0.00")
	source.EXPECT().String("rail_pix_payout", "min_amount_brl").Return("", nil).Once()
	source.EXPECT().String("rail_pix_payout", "max_amount_brl").Return("", nil).Once()
	source.EXPECT().String("rail_pix_payout", "min_amount_brl_by_token").Return("", nil).Once()
	source.EXPECT().String("rail_pix_payout", "ozav_offramp_funding_window_minutes").Return("30", nil).Once()
	source.EXPECT().String("rail_pix_payout", "ozav_deposit_match_tolerance").Return("0", nil).Once()

	policy, err := ramp.NewConfig(source).Rail(ramp.RailPixPayout)
	require.NoError(t, err)
	assert.Empty(t, policy.Providers)
}

// A provider with no fee of its own must never be priced at "": that would
// reach the adapter as "no fee" and quietly give the order away.
func TestRailPolicy_FeeFor(t *testing.T) {
	policy := ramp.RailPolicy{Fees: map[ramp.ProviderCode]string{
		ramp.ProviderBridge: "1.50", ramp.ProviderOzav: "0.80",
	}}

	fee, err := policy.FeeFor(ramp.ProviderOzav)
	require.NoError(t, err)
	assert.Equal(t, "0.80", fee)

	_, err = policy.FeeFor(ramp.ProviderMutualPay)
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

func TestFeeProviderCodesAndKeys(t *testing.T) {
	assert.Equal(t, []ramp.ProviderCode{ramp.ProviderBridge, ramp.ProviderOzav, ramp.ProviderBlindPay}, ramp.FeeProviderCodes())
	assert.Equal(t, "fee_percent_ozav", ramp.FeeSettingKey(ramp.ProviderOzav))
}

func TestConfig_Secret(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Secret("provider_bridge", "api_key").Return("sk_live", nil).Once()

	got, err := ramp.NewConfig(source).Secret(ramp.ProviderBridge, ramp.SettingAPIKey)
	require.NoError(t, err)
	assert.Equal(t, "sk_live", got)
}

// The state a provider can be in, and whether the router may consider it. This
// table came across from the ProviderConfig model, which held the same rule.
func TestProviderState_IsUsable(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		mode    string
		want    bool
	}{
		{"enabled sandbox", true, models.ProviderModeSandbox, true},
		{"enabled production", true, models.ProviderModeProduction, true},
		{"disabled", false, models.ProviderModeProduction, false},
		{"enabled with unknown mode", true, "staging", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := ramp.ProviderState{Enabled: c.enabled, Mode: c.mode}
			assert.Equal(t, c.want, state.IsUsable())
		})
	}
}

// The fan-out needs one list, in a stable order. Order matters because a
// partial failure reports the first provider's error, and "first" has to mean
// the same thing on every call.
func TestEligibleProvidersReturnsOnlyUsableOnesInDeclaredOrder(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return("sandbox", nil).Once()
	source.EXPECT().Bool("provider_mutualpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_mutualpay", "mode").Return("sandbox", nil).Once()
	source.EXPECT().Bool("provider_ozav", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return("sandbox", nil).Once()
	source.EXPECT().Bool("provider_blindpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_blindpay", "mode").Return(models.ProviderModeSandbox, nil).Once()

	codes, err := ramp.NewConfig(source).EligibleProviders("local", nil)

	require.NoError(t, err)
	assert.Equal(t, []ramp.ProviderCode{ramp.ProviderBridge, ramp.ProviderOzav}, codes)
}

// A provider whose mode is nonsense is not usable, and must not silently join
// a fan-out: submitting a customer's identity documents to it would be a real
// side effect bought by a typo in a settings row.
func TestEligibleProvidersSkipsUnusableMode(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return("banana", nil).Once()
	source.EXPECT().Bool("provider_mutualpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_mutualpay", "mode").Return("sandbox", nil).Once()
	source.EXPECT().Bool("provider_ozav", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return("sandbox", nil).Once()
	source.EXPECT().Bool("provider_blindpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_blindpay", "mode").Return(models.ProviderModeSandbox, nil).Once()

	codes, err := ramp.NewConfig(source).EligibleProviders("local", nil)

	require.NoError(t, err)
	assert.Empty(t, codes)
}

// Bridge is usable but its mode disagrees with "production", so it must be
// dropped; Ozav is usable and coherent, and must survive.
func TestEligibleProvidersDropsAUsableProviderWhoseModeDisagreesWithAppEnv(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_mutualpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_mutualpay", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_ozav", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return(models.ProviderModeProduction, nil).Once()
	source.EXPECT().Bool("provider_blindpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_blindpay", "mode").Return(models.ProviderModeSandbox, nil).Once()

	var mismatched []ramp.ProviderCode
	codes, err := ramp.NewConfig(source).EligibleProviders("production", func(code ramp.ProviderCode, _ error) {
		mismatched = append(mismatched, code)
	})

	require.NoError(t, err)
	assert.Equal(t, []ramp.ProviderCode{ramp.ProviderOzav}, codes)
	assert.Equal(t, []ramp.ProviderCode{ramp.ProviderBridge}, mismatched,
		"the mismatch hook must fire for the dropped provider, and only for it")
}

// A provider that was never enabled is not a mismatch worth a caller's
// attention — the hook must fire only for a provider IsUsable already
// accepted, which ValidateMode then refused.
func TestEligibleProvidersNeverCallsTheHookForAProviderThatWasNeverEnabled(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_mutualpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_mutualpay", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_ozav", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_blindpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_blindpay", "mode").Return(models.ProviderModeSandbox, nil).Once()

	codes, err := ramp.NewConfig(source).EligibleProviders("production", func(code ramp.ProviderCode, _ error) {
		t.Fatalf("hook must not fire for a disabled provider, got %q", code)
	})

	require.NoError(t, err)
	assert.Empty(t, codes)
}

// A nil hook is the capabilities read model's own shape: it only reads, so it
// has nothing to log, and passing nil must not panic.
func TestEligibleProvidersAcceptsANilHook(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().Bool("provider_bridge", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_bridge", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_mutualpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_mutualpay", "mode").Return(models.ProviderModeSandbox, nil).Once()
	source.EXPECT().Bool("provider_ozav", "enabled").Return(true, nil).Once()
	source.EXPECT().String("provider_ozav", "mode").Return(models.ProviderModeProduction, nil).Once()
	source.EXPECT().Bool("provider_blindpay", "enabled").Return(false, nil).Once()
	source.EXPECT().String("provider_blindpay", "mode").Return(models.ProviderModeSandbox, nil).Once()

	codes, err := ramp.NewConfig(source).EligibleProviders("production", nil)

	require.NoError(t, err)
	assert.Equal(t, []ramp.ProviderCode{ramp.ProviderOzav}, codes)
}

// A bank rail reads its limits in USD, reads no BRL key and no fee key —
// the strict mock fails the test on any read it was not told about — and an
// unconfigured one comes back closed rather than broken.
func TestConfig_Rail_BankRailLimitsInUSDAndChargesBlindPaysFee(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("rail_ach_payout", "provider_order").Return("", nil).Once()
	source.EXPECT().String("rail_ach_payout", "fee_percent_blindpay").Return("0.00", nil).Once()
	source.EXPECT().String("rail_ach_payout", "min_amount_usd").Return("5.00", nil).Once()
	source.EXPECT().String("rail_ach_payout", "max_amount_usd").Return("", nil).Once()

	policy, err := ramp.NewConfig(source).Rail(ramp.RailACHPayout)
	require.NoError(t, err)
	assert.Empty(t, policy.Providers, "a bank rail nobody configured is closed")
	assert.Equal(t, map[ramp.ProviderCode]string{"blindpay": "0.00"}, policy.Fees)
	require.NotNil(t, policy.MinUSD)
	assert.Equal(t, "5.00", *policy.MinUSD)
	assert.Nil(t, policy.MaxUSD, "an empty limit means unbounded, not zero")
	assert.Nil(t, policy.MinBRL)
	assert.Nil(t, policy.MaxBRL)
}

// A USD payout rail declares a floor in dollars and no BRL limit: the config
// reads only the keys its group has.
func TestConfig_Rail_USDPayoutReadsItsDollarFloor(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("rail_usd_ach_payout", "provider_order").Return("bridge", nil).Once()
	source.EXPECT().String("rail_usd_ach_payout", "fee_percent_bridge").Return("1.00", nil).Once()
	source.EXPECT().String("rail_usd_ach_payout", "fee_percent_ozav").Return("9.99", nil).Once()
	source.EXPECT().String("rail_usd_ach_payout", "min_amount_usd").Return("2.00", nil).Once()

	policy, err := ramp.NewConfig(source).Rail(ramp.RailUSDACHPayout)
	require.NoError(t, err)
	assert.Equal(t, []string{"bridge"}, policy.Providers)
	assert.Equal(t, "1.00", policy.Fees[ramp.ProviderBridge])
	require.NotNil(t, policy.MinUSD)
	assert.Equal(t, "2.00", *policy.MinUSD)
	assert.Nil(t, policy.MinBRL)
	assert.Nil(t, policy.MaxBRL)
}

func TestLimitSettingKeys(t *testing.T) {
	minKey, maxKey := ramp.LimitSettingKeys(ramp.RailPixPayout)
	assert.Equal(t, "min_amount_brl", minKey)
	assert.Equal(t, "max_amount_brl", maxKey)

	minKey, maxKey = ramp.LimitSettingKeys(ramp.RailWirePayout)
	assert.Equal(t, "min_amount_usd", minKey)
	assert.Equal(t, "max_amount_usd", maxKey)
}

func TestFeeProviderCodesFor(t *testing.T) {
	for _, rail := range ramp.PixRails() {
		assert.Equal(t, ramp.FeeProviderCodes(), ramp.FeeProviderCodesFor(rail),
			"the pix rails keep the per-provider fees they already have")
	}
	for _, rail := range ramp.BankRails() {
		assert.Equal(t, []ramp.ProviderCode{ramp.ProviderBlindPay}, ramp.FeeProviderCodesFor(rail),
			"BlindPay is the provider that serves the bank rails")
	}
}

func TestAllProviderCodesListsBlindPayLast(t *testing.T) {
	assert.Equal(t, []ramp.ProviderCode{
		ramp.ProviderBridge, ramp.ProviderMutualPay, ramp.ProviderOzav, ramp.ProviderBlindPay,
	}, ramp.AllProviderCodes())
}

func TestConfig_ProviderString(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("provider_blindpay", "instance_id").Return("in_000000000001", nil).Once()
	value, err := ramp.NewConfig(source).ProviderString(ramp.ProviderBlindPay, ramp.SettingInstanceID)
	require.NoError(t, err)
	assert.Equal(t, "in_000000000001", value)
}

// The USD deposit carries no limit at all: the money arrives before any
// request, so there would be nothing to enforce.
func TestConfig_Rail_USDPayinReadsNoLimit(t *testing.T) {
	source := mocks.NewMockSettingsSource(t)
	source.EXPECT().String("rail_usd_payin", "provider_order").Return("", nil).Once()
	source.EXPECT().String("rail_usd_payin", "fee_percent_bridge").Return("0.00", nil).Once()
	source.EXPECT().String("rail_usd_payin", "fee_percent_ozav").Return("9.99", nil).Once()

	policy, err := ramp.NewConfig(source).Rail(ramp.RailUSDPayin)
	require.NoError(t, err)
	assert.Empty(t, policy.Providers)
	assert.Nil(t, policy.MinUSD)
	assert.Nil(t, policy.MinBRL)
}
