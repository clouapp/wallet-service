package kyc

import "strings"

// XIP's own vocabulary for the risk-profile answers. The app sends these
// keys; each provider adapter translates them into its own words (Ozav's are
// the labels of its hosted form, see ozav/profile_vocabulary.go). Keeping the
// keys here means a label change at a provider never reaches the client.

// SourceOfFundsKeys mirrors the 13 options of Ozav's form, in its order.
var SourceOfFundsKeys = []string{
	"employment_salary", "savings", "investment", "business_income",
	"company_capital", "real_estate", "trust", "loan", "private_capital",
	"winnings", "marital", "grant_government", "other",
}

// AccountPurposeKeys is XIP's own list: the field is free text at Ozav and
// absent from its form.
var AccountPurposeKeys = []string{
	"receive_payments", "send_to_family_friends", "pay_goods_services",
	"hold_crypto", "other",
}

// band is one range of a self-declared USD amount and the single number sent
// for it. Ozav wants a number; its form offers ranges; the number that stands
// for a range is XIP's choice, made here so it can change without an app
// release.
type band struct {
	key string
	usd float64
}

// incomeBands send the midpoint. The first band's floor is 0, which Ozav
// reads as "declared no income" and measures the expected volume against —
// and that band is where most Brazilian users fall. The last band has no
// midpoint and sends its floor.
var incomeBands = []band{
	{"0_1000", 500}, {"1000_5000", 3000}, {"5000_10000", 7500},
	{"10000_50000", 30000}, {"50000_plus", 50000},
}

// volumeBands send the ceiling: it is the amount the economic-capacity
// assessment measures, and the ceiling is the conservative reading. The last
// band has no ceiling and sends its floor.
var volumeBands = []band{
	{"0_5000", 5000}, {"5000_25000", 25000}, {"25000_100000", 100000},
	{"100000_500000", 500000}, {"500000_plus", 500000},
}

var (
	IncomeBandKeys = keysOf(incomeBands)
	VolumeBandKeys = keysOf(volumeBands)
)

func IncomeUSDForBand(key string) (float64, bool) { return usdFor(incomeBands, key) }
func VolumeUSDForBand(key string) (float64, bool) { return usdFor(volumeBands, key) }

func usdFor(bands []band, key string) (float64, bool) {
	for _, b := range bands {
		if b.key == key {
			return b.usd, true
		}
	}
	return 0, false
}

func keysOf(bands []band) []string {
	keys := make([]string, 0, len(bands))
	for _, b := range bands {
		keys = append(keys, b.key)
	}
	return keys
}

// The KYB's activity keys (spec §7.3, API contract §5.2), next to the
// person's: XIP's own words, which each adapter translates. The bands are in
// US dollars for every country — both providers ask in dollars, and a band in
// reais would move with the exchange rate — and the number a band stands for
// is the adapter's to choose, since only one provider takes a number.
var (
	BusinessAccountPurposeKeys = []string{"receive_from_customers", "pay_suppliers", "treasury", "payroll", "other"}
	BusinessSourceOfFundsKeys  = []string{"sales", "owners_capital", "investments", "loans", "intercompany"}
	// AnnualRevenueBandKeys are Bridge's own bands, which nobody else asks.
	AnnualRevenueBandKeys = []string{
		"0_99999", "100000_999999", "1000000_9999999", "10000000_49999999", "50000000_249999999", "250000000_plus",
	}
	MonthlyVolumeBandKeys = []string{"0_9999", "10000_49999", "50000_249999", "250000_999999", "1000000_plus"}
	// BusinessIndustryKeys is the short list of sectors the company step
	// offers, each with one NAICS code at the adapter. There is no "other":
	// the screen asks for the closest one, and a sector with no code would
	// be guessed.
	BusinessIndustryKeys = []string{
		"software_technology", "retail", "wholesale_trade", "professional_services", "marketing_advertising",
		"accounting_legal", "construction", "real_estate", "food_hospitality", "health", "education",
		"transport_logistics", "manufacturing", "agriculture", "media_entertainment",
	}
)

// RuleIn renders a key list as the validator's `in:` rule, so the request
// rules and these tables can never disagree.
func RuleIn(keys []string) string { return "in:" + strings.Join(keys, ",") }
