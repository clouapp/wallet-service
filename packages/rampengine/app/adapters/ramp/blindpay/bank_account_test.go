package blindpay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func achRegistration() ramp.BeneficiaryAccountRegistration {
	return ramp.BeneficiaryAccountRegistration{
		Name: "xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach", Rail: ramp.RailACHPayout,
		Relationship: "first_party", AccountClass: "individual", AccountType: "checking",
		BeneficiaryName: "Ana Souza", RoutingNumber: "021000021", AccountNumber: "123456789",
		AddressLine1: "350 Fifth Avenue", City: "New York", StateCode: "NY", PostalCode: "10118",
	}
}

// bankAccountServer answers POST/GET bank-accounts with body, recording the request.
func bankAccountServer(t *testing.T, status int, body string, seen *[]*http.Request, seenBody *[]byte) (*Adapter, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = append(*seen, r)
		if seenBody != nil && r.Method == http.MethodPost {
			*seenBody = b
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	client := NewClient(Config{APIKey: "k", InstanceID: "in_000000000001", BaseURL: srv.URL})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil, nil), srv.Close
}

func TestRegisterACHSendsTheContractBodyAndChecksTheEcho(t *testing.T) {
	var seen []*http.Request
	var body []byte
	a, done := bankAccountServer(t, 200, `{"id":"ba_000000000001","type":"ach","name":"xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach","status":"verifying","account_number":"123456789","routing_number":"021000021"}`, &seen, &body)
	defer done()

	res, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, achRegistration(), "bank-account:x:1")
	require.NoError(t, err)
	assert.Equal(t, "ba_000000000001", res.ExternalAccountID)
	assert.Equal(t, ramp.BankAccountVerifying, res.Status)
	assert.Equal(t, "6789", res.AccountLast4)
	assert.Equal(t, "0021", res.RoutingLast4)

	require.Len(t, seen, 1)
	assert.Equal(t, "/instances/in_000000000001/customers/re_000000000001/bank-accounts", seen[0].URL.Path)
	assert.Equal(t, "bank-account:x:1", seen[0].Header.Get("Idempotency-Key"))
	var sent map[string]any
	require.NoError(t, json.Unmarshal(body, &sent))
	assert.Equal(t, "ach", sent["type"])
	assert.Equal(t, "checking", sent["account_type"])
	assert.Equal(t, "US", sent["country"])
	assert.Equal(t, "NY", sent["state_province_region"])
	assert.NotContains(t, sent, "business_industry")
	assert.NotContains(t, sent, "address_line_2")
}

func TestRegisterWireOmitsAccountTypeAndBusinessSendsIndustry(t *testing.T) {
	var seen []*http.Request
	var body []byte
	reg := achRegistration()
	reg.Rail, reg.Name = ramp.RailWirePayout, strings.Replace(reg.Name, "-ach", "-wire", 1)
	reg.AccountClass, reg.BusinessIndustry = "business", "541511"
	a, done := bankAccountServer(t, 200, `{"id":"ba_000000000002","type":"wire","name":"`+reg.Name+`","status":"approved","account_number":"123456789","routing_number":"021000021"}`, &seen, &body)
	defer done()

	res, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, reg, "k")
	require.NoError(t, err)
	assert.Equal(t, ramp.BankAccountApproved, res.Status)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(body, &sent))
	assert.Equal(t, "wire", sent["type"])
	assert.NotContains(t, sent, "account_type")
	assert.Equal(t, "541511", sent["business_industry"])
}

func TestTheRegistrationEncodesToTheGoldenBytes(t *testing.T) {
	got, err := encodeBankAccount(achRegistration())
	require.NoError(t, err)
	assert.Equal(t, `{"type":"ach","name":"xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach","recipient_relationship":"first_party","beneficiary_name":"Ana Souza","routing_number":"021000021","account_number":"123456789","account_class":"individual","account_type":"checking","address_line_1":"350 Fifth Avenue","city":"New York","state_province_region":"NY","postal_code":"10118","country":"US"}`, string(got))
}

func TestGetBankAccountReadsStatusAndLast4(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `{"id":"ba_000000000005","type":"ach","name":"n","status":"approved","account_number":"123456789","routing_number":"021000021"}`, &seen, nil)
	defer done()
	res, err := a.GetBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "ba_000000000005")
	require.NoError(t, err)
	assert.Equal(t, ramp.BankAccountApproved, res.Status)
	assert.Equal(t, "6789", res.AccountLast4)
	assert.Equal(t, "0021", res.RoutingLast4)
	require.Len(t, seen, 1)
	assert.Equal(t, "/instances/in_000000000001/customers/re_000000000001/bank-accounts/ba_000000000005", seen[0].URL.Path)
}

func TestGetBankAccountRefusesANonBaIDWithoutACall(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `{}`, &seen, nil)
	defer done()
	_, err := a.GetBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "xx_1")
	require.ErrorIs(t, err, ramp.ErrRequestNotSent)
	assert.Empty(t, seen)
}

func TestFindByNameReturnsTheSingleExactMatch(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `[{"id":"ba_000000000006","type":"ach","name":"xip-mine","status":"approved","account_number":"123456789","routing_number":"021000021"},{"id":"ba_000000000007","type":"ach","name":"xip-other","status":"approved"}]`, &seen, nil)
	defer done()
	res, err := a.FindBankAccountByName(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "xip-mine")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "ba_000000000006", res.ExternalAccountID)
}

func TestFindByNameWithTwoMatchesFailsAsProviderFailure(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `[{"id":"ba_000000000006","name":"xip-mine"},{"id":"ba_000000000007","name":"xip-mine"}]`, &seen, nil)
	defer done()
	_, err := a.FindBankAccountByName(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "xip-mine")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.ErrorIs(t, err, ramp.ErrBankAccountNameAmbiguous)
	assert.NotErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
}

// A refusal of the data carries its HTTP status, so the bank service can tell
// a 422 the user fixes from an outage without importing this package.
func TestARefusedRegistrationCarriesItsStatus(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 422, `{"code":"VALIDATION_ERROR","message":"invalid_routing_number"}`, &seen, nil)
	defer done()
	_, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, achRegistration(), "bank-account:x:1")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	var status interface{ HTTPStatus() int }
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusUnprocessableEntity, status.HTTPStatus())
}

func TestAWrongEchoIsRefusedWithoutTheNumbers(t *testing.T) {
	cases := map[string]string{
		"account last4": `{"id":"ba_000000000003","type":"ach","name":"xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach","status":"approved","account_number":"999999999","routing_number":"021000021"}`,
		"type":          `{"id":"ba_000000000003","type":"wire","name":"xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach","status":"approved","account_number":"123456789","routing_number":"021000021"}`,
		"name":          `{"id":"ba_000000000003","type":"ach","name":"other","status":"approved","account_number":"123456789","routing_number":"021000021"}`,
		"id prefix":     `{"id":"xx_000000000003","type":"ach","name":"xip-6f1c2a9e-1111-4222-8333-944455556666-r1-ach","status":"approved","account_number":"123456789","routing_number":"021000021"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var seen []*http.Request
			a, done := bankAccountServer(t, 200, body, &seen, nil)
			defer done()
			_, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, achRegistration(), "k")
			require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
			assert.NotContains(t, err.Error(), "123456789")
			assert.NotContains(t, err.Error(), "999999999")
		})
	}
}

func TestFindByNameFiltersExactlyAndAnswersNilWhenMissing(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `[{"id":"ba_000000000004","type":"ach","name":"xip-other","status":"approved","account_number":"1","routing_number":"021000021"}]`, &seen, nil)
	defer done()
	res, err := a.FindBankAccountByName(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "xip-mine")
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.Equal(t, "xip-mine", seen[0].URL.Query().Get("name"))
}

func TestDeprecatedAndUnknownStatuses(t *testing.T) {
	assert.Equal(t, ramp.BankAccountDeprecated, bankAccountStatus(strPtr("deprecated")))
	assert.Equal(t, ramp.BankAccountVerifying, bankAccountStatus(nil))
	assert.Equal(t, ramp.BankAccountVerifying, bankAccountStatus(strPtr("whatever")))
}

func TestNoCustomerMeansNoCall(t *testing.T) {
	var seen []*http.Request
	a, done := bankAccountServer(t, 200, `{}`, &seen, nil)
	defer done()
	_, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{}, achRegistration(), "k")
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Empty(t, seen)
}

func strPtr(s string) *string { return &s }

func TestAnUnreadableCreatedAnswerIsAnEchoMismatch(t *testing.T) {
	for name, body := range map[string]string{
		"not json":   `{"id":"ba_0000`,
		"wrong type": `{"id":7,"type":"ach"}`,
		"empty":      ``,
	} {
		t.Run(name, func(t *testing.T) {
			var seen []*http.Request
			a, done := bankAccountServer(t, 200, body, &seen, nil)
			defer done()
			_, err := a.RegisterBankAccount(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, achRegistration(), "k")
			require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
		})
	}
}

func TestFindByNameSkipsDeprecatedAccounts(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"deprecated and approved": {`[{"id":"ba_000000000008","type":"ach","name":"xip-mine","status":"deprecated"},{"id":"ba_000000000009","type":"ach","name":"xip-mine","status":"approved"}]`, "ba_000000000009"},
		"only deprecated":         {`[{"id":"ba_000000000008","type":"ach","name":"xip-mine","status":"deprecated"}]`, ""},
		"rejected is kept":        {`[{"id":"ba_000000000008","type":"ach","name":"xip-mine","status":"deprecated"},{"id":"ba_000000000010","type":"ach","name":"xip-mine","status":"rejected"}]`, "ba_000000000010"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var seen []*http.Request
			a, done := bankAccountServer(t, 200, tc.body, &seen, nil)
			defer done()
			res, err := a.FindBankAccountByName(context.Background(), ramp.CustomerRef{ExternalID: "re_000000000001"}, "xip-mine")
			require.NoError(t, err)
			if tc.want == "" {
				assert.Nil(t, res)
				return
			}
			require.NotNil(t, res)
			assert.Equal(t, tc.want, res.ExternalAccountID)
		})
	}
}
