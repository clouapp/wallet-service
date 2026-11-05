package kyc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Income is sent as the band's midpoint and volume as its ceiling. The floor
// of the first income band is 0, which Ozav reads as "declared no income" —
// and that is the band most Brazilian users fall in. The last band of each
// table has no ceiling and uses its own floor.
func TestIncomeUSDForBand(t *testing.T) {
	cases := map[string]float64{
		"0_1000":      500,
		"1000_5000":   3000,
		"5000_10000":  7500,
		"10000_50000": 30000,
		"50000_plus":  50000,
	}
	for key, want := range cases {
		got, ok := IncomeUSDForBand(key)
		require.True(t, ok, key)
		require.Equal(t, want, got, key)
	}
	_, ok := IncomeUSDForBand("1_2")
	require.False(t, ok)
}

func TestVolumeUSDForBand(t *testing.T) {
	cases := map[string]float64{
		"0_5000":        5000,
		"5000_25000":    25000,
		"25000_100000":  100000,
		"100000_500000": 500000,
		"500000_plus":   500000,
	}
	for key, want := range cases {
		got, ok := VolumeUSDForBand(key)
		require.True(t, ok, key)
		require.Equal(t, want, got, key)
	}
	_, ok := VolumeUSDForBand("")
	require.False(t, ok)
}

func TestVocabularyKeysAndRuleIn(t *testing.T) {
	require.Len(t, SourceOfFundsKeys, 13)
	require.Contains(t, SourceOfFundsKeys, "employment_salary")
	require.Len(t, AccountPurposeKeys, 5)
	require.Equal(t, "in:0_1000,1000_5000,5000_10000,10000_50000,50000_plus", RuleIn(IncomeBandKeys))
	require.Equal(t, IncomeBandKeys, keysOf(incomeBands), "the rule and the table must be the same list")
}

// The KYB's activity keys are XIP's (spec §7.3, API contract §5.2): the app
// sends them, and only the adapters know a provider's words for them. The
// bands are in US dollars for every country, since both providers ask in
// dollars and a band in reais would move with the exchange rate.
func TestBusinessVocabulary(t *testing.T) {
	require.Equal(t, []string{"receive_from_customers", "pay_suppliers", "treasury", "payroll", "other"}, BusinessAccountPurposeKeys)
	require.Equal(t, []string{"sales", "owners_capital", "investments", "loans", "intercompany"}, BusinessSourceOfFundsKeys)
	require.Equal(t, []string{"0_99999", "100000_999999", "1000000_9999999", "10000000_49999999", "50000000_249999999", "250000000_plus"}, AnnualRevenueBandKeys)
	require.Equal(t, []string{"0_9999", "10000_49999", "50000_249999", "250000_999999", "1000000_plus"}, MonthlyVolumeBandKeys)
	require.Equal(t, []string{
		"software_technology", "retail", "wholesale_trade", "professional_services", "marketing_advertising",
		"accounting_legal", "construction", "real_estate", "food_hospitality", "health", "education",
		"transport_logistics", "manufacturing", "agriculture", "media_entertainment",
	}, BusinessIndustryKeys)
	require.NotContains(t, BusinessIndustryKeys, "other", "a sector with no NAICS would be guessed")
}
