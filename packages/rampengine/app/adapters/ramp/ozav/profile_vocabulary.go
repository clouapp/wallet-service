package ozav

import "fmt"

// Ozav's words for XIP's risk-profile keys: its hosted form's own labels,
// which its reviewers read, since the API stores whatever is sent (03/09).
var sourceOfFundsLabels = map[string]string{
	"employment_salary": "Employment / Salary",
	"savings":           "Savings",
	"investment":        "Investment",
	"business_income":   "Company / Business Income",
	"company_capital":   "Company Capital",
	"real_estate":       "Real Estate",
	"trust":             "Trust",
	"loan":              "Loan",
	"private_capital":   "Private Capital",
	"winnings":          "Winnings",
	"marital":           "Marital",
	"grant_government":  "Grant / Government",
	"other":             "Other",
}

// account_purpose has no list on Ozav's form; these are XIP's own answers,
// spelled out for a reviewer.
var accountPurposeLabels = map[string]string{
	"receive_payments":       "Receive payments for work or services",
	"send_to_family_friends": "Send money to family or friends",
	"pay_goods_services":     "Pay for goods and services",
	"hold_crypto":            "Buy and hold crypto",
	"other":                  "Other",
}

func sourceOfFundsLabel(key string) (string, error) {
	return labelFor(sourceOfFundsLabels, "source_of_funds", key)
}
func accountPurposeLabel(key string) (string, error) {
	return labelFor(accountPurposeLabels, "account_purpose", key)
}

// labelFor passes an empty key through (the user may leave the field out) and
// fails on an unknown one rather than forwarding it: the request rules already
// refuse it, so reaching here means the contract was violated upstream.
func labelFor(labels map[string]string, field, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	label, ok := labels[key]
	if !ok {
		return "", fmt.Errorf("ozav: unrecognized %s key %q", field, key)
	}
	return label, nil
}
