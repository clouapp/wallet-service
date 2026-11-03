package blindpay_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestCustomerCreateRequestMatchesTheContract(t *testing.T) {
	requireFieldsInRequestSchema(t, "POST", "/v1/instances/{instance_id}/customers", blindpay.CustomerCreateRequest{})
}

// preparedBody prepares sub against a stub and decodes the body.
func preparedBody(t *testing.T, sub ramp.AtomicKycSubmission) map[string]any {
	t.Helper()
	adapter, _ := newAtomicStub(t)
	var p persisted
	out, err := adapter.PrepareCustomer(context.Background(), sub, ramp.AtomicKycCheckpoint{}, p.persist)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &body))
	return body
}

func TestCustomerCreateRequest_MapsTheSubmission(t *testing.T) {
	body := preparedBody(t, submission(true))
	assert.Equal(t, map[string]any{
		"type":                  "individual",
		"kyc_type":              "standard",
		"email":                 "ana@example.com",
		"tax_id":                "12345678909",
		"first_name":            "Ana",
		"last_name":             "Souza",
		"date_of_birth":         "1990-04-02T00:00:00Z",
		"phone_number":          "+5511987654321",
		"address_line_1":        "Av. Paulista 1000",
		"address_line_2":        "Apto 12, Bela Vista",
		"city":                  "São Paulo",
		"state_province_region": "SP",
		"country":               "BR",
		"postal_code":           "01310-100",
		"ip_address":            "203.0.113.7",
		"tos_id":                "to_1",
		"external_id":           "xip-kyc-42",
		"id_doc_country":        "BR",
		"id_doc_type":           "ID_CARD",
		"id_doc_front_file":     "https://files.blindpay.test/id_doc_front_file.jpg",
		"id_doc_back_file":      "https://files.blindpay.test/id_doc_back_file.png",
		"selfie_file":           "https://files.blindpay.test/selfie_file.png",
		"account_purpose":       "purchase_goods_and_services",
		"occupation":            "Engineer",
		"source_of_wealth":      "salary_or_wages",
	}, body)
}

func TestCustomerCreateRequest_OmitsWhatWasNotDeclared(t *testing.T) {
	sub := submission(false)
	p := &sub.Application.Profile
	p.Address.StreetLine2, p.Address.Number, p.Address.Subdivision = "", "", ""
	p.Occupation, p.SourceOfFunds, p.AccountPurpose = "", "loan", "unknown_key"
	body := preparedBody(t, sub)
	assert.Equal(t, "Av. Paulista", body["address_line_1"])
	assert.Equal(t, "Bela Vista", body["address_line_2"], "no leading separator")
	for _, key := range []string{"id_doc_back_file", "state_province_region", "occupation", "source_of_wealth", "account_purpose"} {
		assert.NotContains(t, body, key)
	}
}

func TestCustomerCreateRequest_Vocabularies(t *testing.T) {
	purposes := map[string]any{
		"receive_payments":       "receive_payments_for_goods_and_services",
		"send_to_family_friends": "payments_to_friends_or_family_abroad",
		"pay_goods_services":     "purchase_goods_and_services",
		"hold_crypto":            "investment_purposes",
		// BlindPay requires account_purpose_other with "other", which XIP does
		// not collect: omitted rather than sent to fail the create.
		"other": nil,
	}
	for key, want := range purposes {
		sub := submission(false)
		sub.Application.Profile.AccountPurpose = key
		assert.Equal(t, want, preparedBody(t, sub)["account_purpose"], key)
	}
	wealth := map[string]any{
		"employment_salary": "salary_or_wages", "savings": "savings", "investment": "investments",
		"business_income": "business_dividends_or_profits", "company_capital": "client_investor_contributions",
		"real_estate": "asset_sales", "trust": "inheritance", "private_capital": "investments", "winnings": "gambling",
		"loan": nil, "marital": nil, "grant_government": nil, "other": nil,
	}
	for key, want := range wealth {
		sub := submission(false)
		sub.Application.Profile.SourceOfFunds = key
		assert.Equal(t, want, preparedBody(t, sub)["source_of_wealth"], key)
	}
	for canonical, want := range map[string]string{"passport": "PASSPORT", "national_id": "ID_CARD", "drivers_license": "DRIVERS"} {
		sub := submission(false)
		sub.Documents.IdentifyingInformation[0].Type = canonical
		assert.Equal(t, want, preparedBody(t, sub)["id_doc_type"], canonical)
	}
}

func TestCustomerCreateRequest_TruncatesOccupation(t *testing.T) {
	sub := submission(false)
	sub.Application.Profile.Occupation = strings.Repeat("é", 300)
	assert.Equal(t, strings.Repeat("é", 255), preparedBody(t, sub)["occupation"])
}

func TestCustomerCreateRequest_USStateIsItsTwoLetterCode(t *testing.T) {
	sub := submission(false)
	sub.Application.Profile.Address.Country, sub.Application.Profile.Address.Subdivision = "USA", "us-ca"
	body := preparedBody(t, sub)
	assert.Equal(t, "CA", body["state_province_region"])
	assert.Equal(t, "US", body["country"])
}

func TestCustomerCreateRequest_PrefersTheApplicationsTaxID(t *testing.T) {
	sub := submission(false)
	sub.Application.Profile.TaxID = "00000000000"
	assert.Equal(t, "12345678909", preparedBody(t, sub)["tax_id"])
}

func TestCustomerCreateRequest_AUSTaxIDPassesThroughUnchanged(t *testing.T) {
	for _, typ := range []string{models.TaxIDTypeSSN, models.TaxIDTypeITIN} {
		sub := submission(false)
		sub.Application.IdentifyingInformation = []ramp.IdentifyingDocument{{Type: typ, IssuingCountry: "USA", Number: "123-45-6789"}}
		assert.Equal(t, "123-45-6789", preparedBody(t, sub)["tax_id"], typ)
	}
}
