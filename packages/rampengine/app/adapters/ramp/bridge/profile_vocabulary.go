package bridge

import (
	"slices"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Bridge's source_of_funds and account_purpose for XIP's risk-profile keys
// (kyc.SourceOfFundsKeys, kyc.AccountPurposeKeys). Bridge asks them of a
// high-risk customer, 60 or older among them, as
// source_of_funds_questionnaire, and source_of_funds alone clears it (sandbox,
// 14/10/2026). A key with no counterpart is left out rather than guessed.
var sourceOfFundsValues = map[string]string{
	"employment_salary": "salary",
	"savings":           "savings",
	"investment":        "investments_loans",
	"business_income":   "company_funds",
	"company_capital":   "company_funds",
	"real_estate":       "sale_of_assets_real_estate",
	"trust":             "inheritance",
	"loan":              "investments_loans",
	"private_capital":   "savings",
	"winnings":          "gambling_proceeds",
	"marital":           "gifts",
	"grant_government":  "government_benefits",
}

// accountPurposeValues maps every XIP purpose; the two Bridge has no word for
// go as "other", which Bridge refuses without account_purpose_other.
var accountPurposeValues = map[string]string{
	"receive_payments":       "other",
	"send_to_family_friends": "payments_to_friends_or_family_abroad",
	"pay_goods_services":     "purchase_goods_and_services",
	"hold_crypto":            "investment_purposes",
	"other":                  "other",
}

// accountPurposeOther is the account_purpose_other a purpose sent as "other"
// carries, in the words of Ozav's labels.
var accountPurposeOther = map[string]string{
	"receive_payments": "Receive payments for work or services",
	"other":            "Other",
}

// The business answers (kyc.Business*Keys) in Bridge's words, from its
// CreateBusinessCustomerPayload enums (18/10/2026). Bridge asks
// estimated_annual_revenue_usd in the bands XIP took from it, so those go as
// they are.
var businessAccountPurposeValues = map[string]string{
	"receive_from_customers": "receive_payments_for_goods_and_services",
	"pay_suppliers":          "purchase_goods_and_services",
	"treasury":               "treasury_management",
	"payroll":                "payroll",
	"other":                  "other",
}

var businessSourceOfFundsValues = map[string]string{
	"sales":          "sales_of_goods_and_services",
	"owners_capital": "owners_capital",
	"investments":    "investment_proceeds",
	"loans":          "business_loans",
	"intercompany":   "inter_company_funds",
}

// businessIndustryValues is one NAICS 2022 code per sector, each on Bridge's
// list of valid codes (business-industry-list-updated-2022-naics-codes, read
// 18/10/2026).
var businessIndustryValues = map[string]string{
	"software_technology":   "541511", // Custom Computer Programming Services
	"retail":                "455219", // All Other General Merchandise Retailers
	"wholesale_trade":       "423990", // Other Miscellaneous Durable Goods Merchant Wholesalers
	"professional_services": "541611", // Administrative Management and General Management Consulting
	"marketing_advertising": "541810", // Advertising Agencies
	"accounting_legal":      "541211", // Offices of Certified Public Accountants
	"construction":          "236220", // Commercial and Institutional Building Construction
	"real_estate":           "531210", // Offices of Real Estate Agents and Brokers
	"food_hospitality":      "722511", // Full-Service Restaurants
	"health":                "621111", // Offices of Physicians
	"education":             "611710", // Educational Support Services
	"transport_logistics":   "488510", // Freight Transportation Arrangement
	"manufacturing":         "339999", // All Other Miscellaneous Manufacturing
	"agriculture":           "111998", // All Other Miscellaneous Crop Farming
	"media_entertainment":   "512110", // Motion Picture and Video Production
}

// monthlyPaymentsUSD is the number expected_monthly_payments_usd takes for
// each band: its ceiling, the conservative reading of what the company will
// move, and for the last band, which has none, its floor.
var monthlyPaymentsUSD = map[string]int{
	"0_9999":        9999,
	"10000_49999":   49999,
	"50000_249999":  249999,
	"250000_999999": 999999,
	"1000000_plus":  1000000,
}

func expectedMonthlyPaymentsUSD(band string) (int, bool) {
	usd, ok := monthlyPaymentsUSD[band]
	return usd, ok
}

// brazilianBusinessTypes are the Brazilian natures as Bridge's types, by
// their liability: an MEI and an EI are sole proprietorships, an SLU and an
// LTDA limited companies, an S.A. a corporation.
var brazilianBusinessTypes = map[string]string{
	models.CompanyTypeMEI:  "sole_prop",
	models.CompanyTypeEI:   "sole_prop",
	models.CompanyTypeSLU:  "llc",
	models.CompanyTypeLTDA: "llc",
	models.CompanyTypeSA:   "corporation",
}

// businessTypeFor is Bridge's business_type for a company type: a Brazilian
// nature mapped, any other one of models.CompanyTypesFor as it is (they are
// Bridge's words), and "" for a type neither knows, left out rather than
// guessed.
func businessTypeFor(companyType string) string {
	if businessType, ok := brazilianBusinessTypes[companyType]; ok {
		return businessType
	}
	if slices.Contains(models.CompanyTypesFor("USA"), companyType) {
		return companyType
	}
	return ""
}
