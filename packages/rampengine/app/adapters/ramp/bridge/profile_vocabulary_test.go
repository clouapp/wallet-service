package bridge

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// Bridge's enums (OpenAPI UpdateIndividualCustomerPayload, read 14/10/2026).
var (
	bridgeSourceOfFunds = []string{"company_funds", "ecommerce_reseller", "gambling_proceeds", "gifts",
		"government_benefits", "inheritance", "investments_loans", "pension_retirement", "salary",
		"sale_of_assets_real_estate", "savings", "someone_elses_funds"}
	bridgeAccountPurposes = []string{"charitable_donations", "ecommerce_retail_payments", "investment_purposes",
		"operating_a_company", "other", "payments_to_friends_or_family_abroad", "personal_or_living_expenses",
		"protect_wealth", "purchase_goods_and_services", "receive_payment_for_freelancing", "receive_salary"}
)

// Every XIP key Bridge has a word for maps onto a value of Bridge's enum — a
// value outside it would be a 400 for every customer — and "other", which
// Bridge's source_of_funds has no word for, maps to nothing.
func TestProfileVocabularyMapsOntoBridgesEnums(t *testing.T) {
	for _, key := range kycsvc.SourceOfFundsKeys {
		value, ok := sourceOfFundsValues[key]
		if key == "other" {
			require.False(t, ok, "Bridge has no 'other' source of funds")
			continue
		}
		require.True(t, ok, key)
		require.True(t, slices.Contains(bridgeSourceOfFunds, value), "%s → %s", key, value)
	}
	for _, key := range kycsvc.AccountPurposeKeys {
		value, ok := accountPurposeValues[key]
		require.True(t, ok, key)
		require.True(t, slices.Contains(bridgeAccountPurposes, value), "%s → %s", key, value)
		if value == "other" {
			require.NotEmpty(t, accountPurposeOther[key], "Bridge refuses 'other' without account_purpose_other: %s", key)
		}
	}
}

func TestSubmitApplicationAnswersTheSourceOfFundsQuestionnaire(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
	})
	app := usApplication()
	app.Profile.SourceOfFunds = "employment_salary"
	app.Profile.AccountPurpose = "send_to_family_friends"

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)

	require.Equal(t, "salary", got.body["source_of_funds"])
	require.Equal(t, "payments_to_friends_or_family_abroad", got.body["account_purpose"])
	require.NotContains(t, got.body, "account_purpose_other")
}

// The questionnaire goes to every Bridge customer, not only a US resident.
func TestSubmitApplicationAnswersTheQuestionnaireForABrazilianToo(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
	})
	app := fullApplication()
	app.Profile.SourceOfFunds = "savings"
	app.Profile.AccountPurpose = "hold_crypto"

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)

	require.Equal(t, "savings", got.body["source_of_funds"])
	require.Equal(t, "investment_purposes", got.body["account_purpose"])
	require.NotContains(t, got.body, "account_purpose_other")
}

// A profile with no answers — an installed build's — sends none of the
// questionnaire's keys, wherever the customer lives.
func TestSubmitApplicationWithoutAnswersOmitsTheQuestionnaire(t *testing.T) {
	for name, app := range map[string]ramp.Application{"brazil": fullApplication(), "united states": usApplication()} {
		t.Run(name, func(t *testing.T) {
			adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
			})
			app.Profile.SourceOfFunds, app.Profile.AccountPurpose = "", ""

			_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
			require.NoError(t, err)

			for _, key := range []string{"source_of_funds", "account_purpose", "account_purpose_other"} {
				require.NotContains(t, got.body, key)
			}
		})
	}
}

func TestSubmitApplicationSpellsOutAnOtherPurpose(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
	})
	app := usApplication()
	app.Profile.SourceOfFunds = "other"
	app.Profile.AccountPurpose = "receive_payments"

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)

	require.NotContains(t, got.body, "source_of_funds", "no word for it at Bridge: left out")
	require.Equal(t, "other", got.body["account_purpose"])
	require.Equal(t, "Receive payments for work or services", got.body["account_purpose_other"])
}

// Bridge's business enums (OpenAPI CreateBusinessCustomerPayload, read
// 18/10/2026).
var (
	bridgeBusinessAccountPurposes = []string{"charitable_donations", "ecommerce_retail_payments", "investment_purposes",
		"other", "payments_to_friends_or_family_abroad", "payroll", "personal_or_living_expenses", "protect_wealth",
		"purchase_goods_and_services", "receive_payments_for_goods_and_services", "tax_optimization",
		"third_party_money_transmission", "treasury_management"}
	bridgeBusinessSourceOfFunds = []string{"business_loans", "grants", "inter_company_funds", "investment_proceeds",
		"legal_settlement", "owners_capital", "pension_retirement", "sale_of_assets", "sales_of_goods_and_services",
		"third_party_funds", "treasury_reserves"}
	bridgeAnnualRevenues = []string{"0_99999", "100000_999999", "1000000_9999999", "10000000_49999999",
		"50000000_249999999", "250000000_plus"}
	bridgeBusinessTypes = []string{"cooperative", "corporation", "llc", "other", "partnership", "sole_prop", "trust"}
	// bridgeIndustries are the codes the XIP sectors map to, each checked
	// against Bridge's list of valid NAICS 2022 codes on 18/10/2026
	// (business-industry-list-updated-2022-naics-codes), with its name there.
	bridgeIndustries = map[string]string{
		"541511": "Custom Computer Programming Services",
		"455219": "All Other General Merchandise Retailers",
		"423990": "Other Miscellaneous Durable Goods Merchant Wholesalers",
		"541611": "Administrative Management And General Management Consulting Services",
		"541810": "Advertising Agencies",
		"541211": "Offices Of Certified Public Accountants",
		"236220": "Commercial And Institutional Building Construction",
		"531210": "Offices Of Real Estate Agents And Brokers",
		"722511": "Full Service Restaurants",
		"621111": "Offices Of Physicians Except Mental Health Specialists",
		"611710": "Educational Support Services",
		"488510": "Freight Transportation Arrangement",
		"339999": "All Other Miscellaneous Manufacturing",
		"111998": "All Other Miscellaneous Crop Farming",
		"512110": "Motion Picture And Video Production",
	}
)

// Every XIP business key maps onto a value of Bridge's enum: one outside it
// is a 400 for every company.
func TestBusinessVocabularyMapsOntoBridgesEnums(t *testing.T) {
	for _, key := range kycsvc.BusinessAccountPurposeKeys {
		require.Contains(t, bridgeBusinessAccountPurposes, businessAccountPurposeValues[key], key)
	}
	for _, key := range kycsvc.BusinessSourceOfFundsKeys {
		require.Contains(t, bridgeBusinessSourceOfFunds, businessSourceOfFundsValues[key], key)
	}
	for _, key := range kycsvc.AnnualRevenueBandKeys {
		require.Contains(t, bridgeAnnualRevenues, key, "the revenue bands are Bridge's own")
	}
	require.Len(t, businessIndustryValues, len(kycsvc.BusinessIndustryKeys))
	for _, key := range kycsvc.BusinessIndustryKeys {
		code, ok := businessIndustryValues[key]
		require.True(t, ok, key)
		require.Contains(t, bridgeIndustries, code, "%s → %s is on Bridge's list", key, code)
	}
	for _, country := range []string{"BRA", "USA"} {
		for _, companyType := range models.CompanyTypesFor(country) {
			require.Contains(t, bridgeBusinessTypes, businessTypeFor(companyType), companyType)
		}
	}
}

// The Brazilian natures are Bridge's types by their liability (spec §7.2):
// an MEI and an EI are sole proprietorships, an SLU and an LTDA limited
// companies, an S.A. a corporation. Elsewhere the type is Bridge's word.
func TestBusinessTypeFor(t *testing.T) {
	for companyType, want := range map[string]string{
		"mei": "sole_prop", "ei": "sole_prop", "slu": "llc", "ltda": "llc", "sa": "corporation", "other": "other",
		"corporation": "corporation", "llc": "llc", "partnership": "partnership", "sole_prop": "sole_prop",
		"cooperative": "cooperative", "trust": "trust",
	} {
		require.Equal(t, want, businessTypeFor(companyType), companyType)
	}
	require.Empty(t, businessTypeFor("eireli"), "an unknown type is left out, not guessed")
}

// expected_monthly_payments_usd is a number: a band sends its ceiling, the
// conservative reading of what the company will move, and the last band its
// floor.
func TestExpectedMonthlyPaymentsUSD(t *testing.T) {
	for band, want := range map[string]int{
		"0_9999": 9999, "10000_49999": 49999, "50000_249999": 249999, "250000_999999": 999999, "1000000_plus": 1000000,
	} {
		got, ok := expectedMonthlyPaymentsUSD(band)
		require.True(t, ok, band)
		require.Equal(t, want, got, band)
	}
	_, ok := expectedMonthlyPaymentsUSD("1_2")
	require.False(t, ok)
	require.Len(t, kycsvc.MonthlyVolumeBandKeys, 5)
	for _, band := range kycsvc.MonthlyVolumeBandKeys {
		_, ok := expectedMonthlyPaymentsUSD(band)
		require.True(t, ok, band)
	}
}
