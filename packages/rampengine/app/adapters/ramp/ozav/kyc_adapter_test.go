package ozav

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// capture records what the adapter actually put on the wire, which is the
// only thing most of these tests are about: the adapter is a mapper, so the
// assertions are on the request it builds and the struct it returns.
type capture struct {
	method string
	path   string
	body   map[string]any
	header http.Header
}

// newTestAdapter builds an Adapter whose client points at an httptest
// server, reaching into the unexported clients field directly — Config
// deliberately carries no BaseURL (see client.go), so the transport is
// assembled the same way newTestClient assembles one in client_test.go.
func newTestAdapter(t *testing.T, handler func(*capture, http.ResponseWriter)) (*Adapter, *capture) {
	t.Helper()
	got := &capture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.header = r.Method, r.URL.Path, r.Header
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "application/json")
		handler(got, w)
	}))
	t.Cleanup(server.Close)

	cfg := Config{APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second}
	client := NewClient(Config{APIKey: cfg.APIKey, Timeout: cfg.Timeout, BaseURL: server.URL})
	return &Adapter{clients: func(context.Context) (*Client, error) { return client, nil }}, got
}

// unreachableAdapter fails the test if any HTTP call happens. Guard
// rejections have to be provably local — a wasted upstream call on a doomed
// request is what the guard exists to prevent.
func unreachableAdapter(t *testing.T) *Adapter {
	t.Helper()
	return &Adapter{clients: func(context.Context) (*Client, error) {
		t.Fatal("adapter must reject locally, without calling Ozav")
		return nil, nil
	}}
}

func fullApplication() ramp.Application {
	return ramp.Application{
		Type: "individual",
		Profile: ramp.Profile{
			LegalName:  "Maria Silva",
			MotherName: "Joana Silva",
			Email:      "maria@example.com",
			Phone:      "+5511999999999",
			BirthDate:  "1990-04-02",
			Address: ramp.Address{
				StreetLine1: "Rua A, 100", StreetLine2: "Apto 4",
				Number: "100", Neighborhood: "Centro",
				City: "Sao Paulo", Subdivision: "SP",
				PostalCode: "01000-000", Country: "BRA",
			},
		},
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "cpf", IssuingCountry: "bra", Number: "39053344705"},
			{Type: "national_id", IssuingCountry: "bra", ImageFront: "data:image/jpeg;base64,AAAA"},
		},
		IdempotencyKey: "app-key-1-1234567890",
	}
}

// approvedCustomerBody is the real POST /customers 201 body captured against
// the sandbox (see the plan's "Fatos medidos" section), with placeholder
// PII swapped for test values.
const approvedCustomerBody = `{
	"id":"ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF","object":"customer","type":"individual",
	"status":"approved","email":"maria@example.com",
	"individual":{"legal_name":"Maria Silva","tax_id":{"type":"cpf","value":"39053344705","country":"BR"},
		"residential_address":{"line1":"Rua A, 100","number":"100","neighborhood":"Centro",
			"city":"Sao Paulo","state":"SP","postal_code":"01000-000","country":"BR"}},
	"verification":{"mode":"full","status":"approved","checks":{}},
	"requirements":{"due":[],"missing_fields":["monthly_income_usd","is_pep"]},
	"created_at":"2026-08-25T00:00:00Z","updated_at":"2026-08-25T00:00:00Z"
}`

func TestSubmitApplicationCreatesCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers", got.path, "no external id yet: the customer is born here")
	require.Equal(t, "app-key-1-1234567890", got.header.Get("Idempotency-Key"))

	require.Equal(t, "individual", got.body["type"])
	require.Equal(t, "maria@example.com", got.body["email"])

	individual, ok := got.body["individual"].(map[string]any)
	require.True(t, ok, "the profile must nest under individual")
	require.Equal(t, "Maria Silva", individual["legal_name"])
	require.Equal(t, "Joana Silva", individual["mother_name"])
	require.Equal(t, "1990-04-02", individual["date_of_birth"])
	// not supplied → not sent: fullApplication() leaves the risk profile
	// empty, and an empty field must never appear invented in the body.
	require.NotContains(t, individual, "occupation")
	require.NotContains(t, individual, "source_of_funds")
	require.NotContains(t, individual, "account_purpose")
	require.NotContains(t, individual, "monthly_income_usd")
	require.NotContains(t, individual, "expected_monthly_volume_usd")
	require.NotContains(t, individual, "is_pep")
	require.NotContains(t, individual, "nationality")

	phone, ok := individual["phone"].(map[string]any)
	require.True(t, ok, "phone must be split into country_code/number")
	require.Equal(t, "+55", phone["country_code"])
	require.Equal(t, "11999999999", phone["number"])

	taxID, ok := individual["tax_id"].(map[string]any)
	require.True(t, ok, "the cpf document must become tax_id")
	require.Equal(t, "BR", taxID["country"])
	require.Equal(t, "cpf", taxID["type"])
	require.Equal(t, "39053344705", taxID["value"])

	address, ok := individual["residential_address"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "100", address["number"], "Address.Number must reach residential_address.number")
	require.Equal(t, "Centro", address["neighborhood"], "Address.Neighborhood must reach residential_address.neighborhood")
	require.Equal(t, "SP", address["state"])
	require.Equal(t, "01000-000", address["postal_code"])
	require.Equal(t, "BR", address["country"], "BRA must be converted to alpha-2 BR")
	require.NotContains(t, address, "line2", "Ozav's schema has no line2 to put StreetLine2 in")

	require.Equal(t, "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF", state.ExternalCustomerID)
	require.Equal(t, models.KycStatusApproved, state.Status)
	require.Empty(t, state.TosStatus,
		"the customer object does not echo the terms session, so the row's own value stands")
	require.Len(t, state.Endorsements, 1)
	require.Equal(t, "pix", state.Endorsements[0].Name)
	require.Equal(t, models.EndorsementStatusApproved, state.Endorsements[0].Status)
	require.Equal(t, []string{"monthly_income_usd", "is_pep"}, state.Requirements)
	require.NotEmpty(t, state.Raw)
}

func riskProfile() ramp.Profile {
	income, volume, pep := 500.0, 5000.0, false
	p := fullApplication().Profile
	p.Nationality = "BRA"
	p.Occupation = "Software developer"
	p.SourceOfFunds = "business_income"
	p.AccountPurpose = "receive_payments"
	p.MonthlyIncomeUSD = &income
	p.ExpectedMonthlyVolumeUSD = &volume
	p.IsPEP = &pep
	return p
}

// The seven fields Ozav lists in missing_fields for an individual, on the
// wire in Ozav's own words: the labels of its hosted form for the two
// vocabularies (it stores them verbatim, measured 03/09), alpha-2 for the
// nationality, and is_pep present even when false — an omitted value is
// Ozav's default, a false one is the user's declaration.
func TestSubmitApplicationSendsTheRiskProfile(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvedCustomerBody))
	})
	app := fullApplication()
	app.Profile = riskProfile()

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)

	individual := got.body["individual"].(map[string]any)
	require.Equal(t, "BR", individual["nationality"])
	require.Equal(t, "Software developer", individual["occupation"])
	require.Equal(t, "Company / Business Income", individual["source_of_funds"])
	require.Equal(t, "Receive payments for work or services", individual["account_purpose"])
	require.Equal(t, 500.0, individual["monthly_income_usd"])
	require.Equal(t, 5000.0, individual["expected_monthly_volume_usd"])
	require.Equal(t, false, individual["is_pep"])
}

// Zero income is an answer ("declared no income"), not an absence.
func TestSubmitApplicationSendsZeroIncomeWhenDeclared(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvedCustomerBody))
	})
	app := fullApplication()
	zero := 0.0
	app.Profile.MonthlyIncomeUSD = &zero

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)
	individual := got.body["individual"].(map[string]any)
	require.Contains(t, individual, "monthly_income_usd")
	require.Equal(t, 0.0, individual["monthly_income_usd"])
}

func TestSubmitApplicationRejectsUnknownVocabularyLocally(t *testing.T) {
	app := fullApplication()
	app.Profile.SourceOfFunds = "lottery_of_life"
	_, err := unreachableAdapter(t).SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.Error(t, err)
	require.Contains(t, err.Error(), "source_of_funds")
}

func TestSubmitApplicationRejectsUnknownNationalityLocally(t *testing.T) {
	app := fullApplication()
	app.Profile.Nationality = "XXX"
	_, err := unreachableAdapter(t).SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.Error(t, err)
}

func TestSubmitApplicationUpdatesExistingCustomer(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF"}
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.SubmitApplication(context.Background(), ref, fullApplication())
	require.NoError(t, err)

	require.Equal(t, http.MethodPatch, got.method)
	require.Equal(t, "/customers/ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF", got.path)
}

// PATCH /customers refuses `type`: measured 01/09, 400 INVALID_REQUEST with
// details naming "Unrecognized key(s) in object: 'type'". The customer's type
// is fixed when it is created, so the update has nothing to say about it —
// and sending it made every Ozav resubmission fail, which is the path a user
// takes after a rejection.
func TestSubmitApplicationOmitsTypeOnUpdate(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF"}
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.SubmitApplication(context.Background(), ref, fullApplication())
	require.NoError(t, err)
	require.NotContains(t, got.body, "type")
}

// The create still declares it: Ozav requires `type` to know which identity
// block to enforce.
func TestSubmitApplicationSendsTypeOnCreate(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "individual", got.body["type"])
}

// individual.legal_name is the only field Ozav's sandbox marks required on
// CustomerIndividual (found by the first live smoke: a submission that
// carries FirstName/LastName but no LegalName was rejected with
// INVALID_REQUEST on exactly this field). ramp.Profile.LegalName is never
// populated by any caller in this repo today — applicationFrom in the KYC
// service only fills FirstName/LastName — so the adapter must be able to
// produce a legal name from those instead of forwarding an empty field.
func TestSubmitApplicationSynthesizesLegalName(t *testing.T) {
	cases := []struct {
		name                   string
		legalName, first, last string
		wantLegalName          string
	}{
		{
			name: "existing legal name wins over first/last", legalName: "Maria Isabel Silva",
			first: "Maria", last: "Silva", wantLegalName: "Maria Isabel Silva",
		},
		{
			name:  "synthesizes from first and last when legal name is empty",
			first: "Luiza", last: "Souza", wantLegalName: "Luiza Souza",
		},
		{
			name:  "synthesizes from first name alone, no trailing space",
			first: "Luiza", wantLegalName: "Luiza",
		},
		{
			name: "synthesizes from last name alone, no leading space",
			last: "Souza", wantLegalName: "Souza",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := ramp.Application{
				Type: "individual",
				Profile: ramp.Profile{
					LegalName: tc.legalName,
					FirstName: tc.first,
					LastName:  tc.last,
					Email:     "x@example.com",
				},
			}
			adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"id":"cus_x","status":"pending","requirements":{"due":[],"missing_fields":[]}}`))
			})

			_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
			require.NoError(t, err)

			individual, ok := got.body["individual"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tc.wantLegalName, individual["legal_name"])
		})
	}
}

// The core safety property behind "falhe alto": a country this adapter
// cannot convert must never reach Ozav as a guess, and the rejection must be
// provably local.
func TestSubmitApplicationRejectsUnknownCountryLocally(t *testing.T) {
	app := fullApplication()
	app.Profile.Address.Country = "XXX"

	_, err := unreachableAdapter(t).SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
	require.Error(t, err)
}

// The same "falhe alto" property for the phone, and the bug it was written
// for: a phone that is not in the E.164 the contract promises used to reach
// Ozav as an individual.phone with a blank country_code, and came back as a
// rejection naming individual.phone.country_code — a field the client never
// sent, on a request that looked fine locally. The refusal must be provably
// local now, and never a half-filled payload.
func TestSubmitApplicationRejectsUnsplittablePhoneLocally(t *testing.T) {
	for _, phone := range []string{"11999999999", "+99912345678"} {
		t.Run(phone, func(t *testing.T) {
			app := fullApplication()
			app.Profile.Phone = phone

			_, err := unreachableAdapter(t).SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
			require.Error(t, err)
		})
	}
}

// An application with no phone yet must not fail over one it never supplied —
// same reasoning as the address below.
func TestSubmitApplicationOmitsPhoneWhenNotSupplied(t *testing.T) {
	app := fullApplication()
	app.Profile.Phone = ""

	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_x","status":"pending","requirements":{"due":[],"missing_fields":[]}}`))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
	require.NoError(t, err)

	individual, ok := got.body["individual"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, individual, "phone")
}

// An application with no address yet must not fail over a country it never
// supplied — omission is different from an unconvertible value.
func TestSubmitApplicationOmitsAddressWhenNotSupplied(t *testing.T) {
	app := ramp.Application{
		Type:    "individual",
		Profile: ramp.Profile{Email: "x@example.com"},
	}
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_x","status":"pending","requirements":{"due":[],"missing_fields":[]}}`))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
	require.NoError(t, err)

	individual, ok := got.body["individual"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, individual, "residential_address")
}

func TestGetKycMapsApprovedCustomer(t *testing.T) {
	// The paths are collected rather than read off the shared capture: GetKyc
	// makes two calls now — the customer, then the documents it is still
	// waiting on — and the capture holds only the last.
	var paths []string
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		paths = append(paths, got.method+" "+got.path)
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{ExternalID: "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"GET /customers/ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF",
		"GET /customers/ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF/documents",
	}, paths)
	require.Equal(t, models.KycStatusApproved, state.Status)
	require.Equal(t, models.EndorsementStatusApproved, state.Endorsements[0].Status)
}

// The rule status_map.go enforces: an approved status with no verification
// mode on record is not the same claim as one Ozav actually vetted.
func TestGetKycDowngradesApprovedWithoutVerificationMode(t *testing.T) {
	body := `{"id":"cus_1","status":"approved","requirements":{"due":[],"missing_fields":[]}}`
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(body))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusUnderReview, state.Status)
	require.Equal(t, models.EndorsementStatusIncomplete, state.Endorsements[0].Status,
		"an approved status with no verification mode must not clear the rail either")
}

func TestRefreshKycReadsTheSameEndpointAsGetKyc(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.RefreshKyc(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, got.method, "Ozav has no separate refresh endpoint")
}

func TestGetRequirementsCollectsDueAndMissingFields(t *testing.T) {
	body := `{"id":"cus_1","status":"in_review",
		"requirements":{"due":["selfie"],"missing_fields":["selfie","monthly_income_usd"]}}`
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(body))
	})

	reqs, err := adapter.GetRequirements(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, models.KycStatusIncomplete, reqs.Status, "in review, waiting on fields only the user can supply")
	require.Equal(t, []string{"selfie", "monthly_income_usd"}, reqs.Missing, "due and missing_fields dedupe, due first")
	require.Len(t, reqs.Endorsements, 1)
	require.Equal(t, models.EndorsementStatusIncomplete, reqs.Endorsements[0].Status)
}

// The scoped wizard reads its steps from here, so it has to see the same files
// GetKyc does: without them a customer Ozav's compliance is chasing for a bank
// statement opened a wizard with nothing in it.
func TestGetRequirementsMergesTheDocumentsOzavIsWaitingOn(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":["proof_of_address","bank_statement"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"in_review","verification":{"mode":"reliance"},
			"requirements":{"due":[],"missing_fields":["occupation"],"owner":"ozav"}}`))
	})

	reqs, err := adapter.GetRequirements(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, []string{"occupation", "proof_of_address", "bank_statement"}, reqs.Missing)
}

func TestStartIntegrationIsLocalWhenNoExternalID(t *testing.T) {
	integration, err := unreachableAdapter(t).StartIntegration(context.Background(), ramp.CustomerRef{CustomerID: 9})
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusPending, integration.Status)
	require.Empty(t, integration.ExternalCustomerID)
}

func TestStartIntegrationDelegatesToRefreshWhenExternalIDPresent(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	integration, err := adapter.StartIntegration(context.Background(),
		ramp.CustomerRef{ExternalID: "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF"})
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, models.IntegrationStatusActive, integration.Status)
	require.Equal(t, "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF", integration.ExternalCustomerID)
}

// Ozav has no "offboarded" terminal customer state: a rejected or suspended
// customer is still a working link, so the integration stays active either
// way. Whether the user may transact is the KYC row's business.
func TestRefreshIntegrationIsActiveRegardlessOfCustomerStatus(t *testing.T) {
	for _, status := range []string{"pending", "in_review", "approved", "rejected", "suspended"} {
		t.Run(status, func(t *testing.T) {
			body := `{"id":"cus_1","status":"` + status + `","requirements":{"due":[],"missing_fields":[]}}`
			adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				_, _ = w.Write([]byte(body))
			})

			integration, err := adapter.RefreshIntegration(context.Background(), ramp.CustomerRef{ExternalID: "cus_1"})
			require.NoError(t, err)
			require.Equal(t, models.IntegrationStatusActive, integration.Status)
		})
	}
}

func TestCountryAlpha2(t *testing.T) {
	code, err := countryAlpha2("BRA")
	require.NoError(t, err)
	require.Equal(t, "BR", code)

	code, err = countryAlpha2("bra")
	require.NoError(t, err, "conversion is case-insensitive")
	require.Equal(t, "BR", code)

	_, err = countryAlpha2("ZZZ")
	require.Error(t, err, "an unrecognized code must fail loudly, never guess")
	require.EqualError(t, err, `ozav: unrecognized country code "ZZZ"`)
}

func TestCountryAlpha2CoversCountriesBeyondTheOldHardcodedList(t *testing.T) {
	for alpha3, want := range map[string]string{"ARG": "AR", "JPN": "JP", "PRT": "PT", "ZAF": "ZA", "pt": "PT"} {
		code, err := countryAlpha2(alpha3)
		require.NoError(t, err, alpha3)
		require.Equal(t, want, code, alpha3)
	}
	code, err := optionalAlpha2("")
	require.NoError(t, err)
	require.Empty(t, code)
}

func TestTaxIDFromRequiresACpfTypedDocument(t *testing.T) {
	require.Nil(t, taxIDFrom(nil))
	require.Nil(t, taxIDFrom([]ramp.IdentifyingDocument{{Type: "national_id", Number: "123"}}),
		"only a cpf-typed document is a tax id candidate")

	taxID := taxIDFrom([]ramp.IdentifyingDocument{
		{Type: "national_id", Number: "123"},
		{Type: "cpf", Number: "39053344705"},
	})
	require.NotNil(t, taxID)
	require.Equal(t, "BR", taxID.Country)
	require.Equal(t, "cpf", taxID.Type)
	require.Equal(t, "39053344705", taxID.Value)
}

func TestPhoneFrom(t *testing.T) {
	absent, err := phoneFrom("")
	require.NoError(t, err)
	require.Nil(t, absent, "a phone the customer has not supplied is an omitted field, not an error")

	brazilian, err := phoneFrom("+5511999999999")
	require.NoError(t, err)
	require.NotNil(t, brazilian)
	require.Equal(t, "+55", brazilian.CountryCode)
	require.Equal(t, "11999999999", brazilian.Number)

	american, err := phoneFrom("+14155552671")
	require.NoError(t, err)
	require.NotNil(t, american)
	require.Equal(t, "+1", american.CountryCode)
	require.Equal(t, "4155552671", american.Number)

	// This used to return &phonePayload{Number: "+1 555 0100"} with a blank
	// CountryCode, and blank is what Ozav rejects. Refusing here is what keeps
	// the rejection readable and local.
	for _, unsplittable := range []string{"+1 555 0100", "11999999999", "+99912345678"} {
		got, err := phoneFrom(unsplittable)
		require.Error(t, err, "phoneFrom(%q) must refuse rather than blank the country code", unsplittable)
		require.Nil(t, got)
	}
}

func TestRequirementsFromDedupesDueAndMissingFields(t *testing.T) {
	got := requirementsFrom(customerRequirements{
		Due:           []string{"selfie", "proof_of_address"},
		MissingFields: []string{"selfie", "monthly_income_usd"},
	})
	require.Equal(t, []string{"selfie", "proof_of_address", "monthly_income_usd"}, got)
}

// Custody is XIP's to choose since 28/08, and XIP chooses managed. Ozav's
// `custody_model` decides it per customer; omitting the field would hand the
// choice back to the organization's default, which is self_custody while that
// product is enabled — and a self-custody customer can never off-ramp, having
// neither a deposit address nor, in Ozav's own words, a "custody balance for
// an off-ramp to debit". The user still holds their keys: the deposit is
// attributed by destination address, so they fund the wallet from the wallet
// the XIP app already created for them.
func TestSubmitApplicationAsksForManagedCustody(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)

	require.Equal(t, "managed", got.body["custody_model"])
	require.NotContains(t, got.body, "self_custody", "managed custody registers no wallet of the user's")
}

// And only on the create. Ozav cannot convert an existing customer's custody
// model — registering a self-custody address on a managed customer answers
// 409 WALLET_ALREADY_MANAGED, "contact OZAV support to migrate custody" — so a
// PATCH naming a model is asking for something the API cannot do.
func TestSubmitApplicationOmitsCustodyModelOnUpdate(t *testing.T) {
	ref := ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_01M0WR18MQV8H9Q2E3GD4EPPQF"}

	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(approvedCustomerBody))
	})

	_, err := adapter.SubmitApplication(context.Background(), ref, fullApplication())
	require.NoError(t, err)

	require.Equal(t, http.MethodPatch, got.method)
	require.NotContains(t, got.body, "custody_model", "custody cannot be converted by PATCH")
}

// The customer object says nothing about documents. What the person still
// owes in files lives on GET /customers/{id}/documents, under
// `still_required`, and reading only the customer meant an empty
// `requirements` for someone Ozav was plainly waiting on — which the app
// reads as "nothing outstanding" and answers by asking for everything again
// from the top, name and address included.
func TestGetKycMergesTheDocumentsStillRequired(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":["identity_document","proof_of_address"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"pending","verification":{"mode":"full"},
			"requirements":{"due":[],"missing_fields":[]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err)
	require.Contains(t, state.Requirements, "identity_document")
	require.Contains(t, state.Requirements, "proof_of_address")
}

// The documents listing is extra information, not the answer itself. Losing
// the whole read because a second call failed would take the status with it,
// and a status is what every screen renders from.
func TestGetKycSurvivesADocumentsListingThatFails(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"type":"api_error","code":"INTERNAL_ERROR"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"approved","verification":{"mode":"full"},
			"requirements":{"due":[],"missing_fields":["occupation"]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err, "a customer that answered is an answer")
	require.Equal(t, models.KycStatusApproved, state.Status)
	require.Equal(t, []string{"occupation"}, state.Requirements)
}

// documents_due is Ozav's compliance asking for a file by name, with a deadline
// — the bank statement it chased a real customer for on 28/09/2026, while
// missing_fields was empty and still_required was not read by anyone. A line
// already submitted or accepted is not owed.
func TestGetKycReadsTheDocumentsComplianceAskedFor(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"in_review","verification":{"mode":"reliance"},
			"requirements":{"due":[],"missing_fields":[],"owner":"integrator","documents_due":[
				{"key":"bank_statement","doc_type":"bank_statement","label":"Extrato bancário","status":"due","reason_code":"capacity_unproven"},
				{"key":"proof_of_address","doc_type":"proof_of_address","label":"Comprovante","status":"submitted"},
				{"key":"selfie","doc_type":"selfie","label":"Selfie","status":"accepted"}]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err)
	require.Equal(t, []string{"bank_statement"}, state.Requirements)
	require.Equal(t, models.KycStatusIncomplete, state.Status,
		"in review, but waiting on a file only the user can send")
}

// Duplicates would show the user the same step twice on a provider that
// happens to name a document in both places.
func TestGetKycDoesNotRepeatARequirementTheCustomerAlreadyNamed(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":["identity_document"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"pending","verification":{"mode":"full"},
			"requirements":{"due":[],"missing_fields":["identity_document"]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})
	require.NoError(t, err)
	require.Equal(t, []string{"identity_document"}, state.Requirements)
}

// Ozav's still_required names a proof of address and a bank statement for
// every individual, so the wizard collects both in the pass that creates the
// customer instead of sending the user back for them (decided with Luiz on
// 28/09/2026 for the statement). Measured on staging the same night: a
// Bridge-approved user whose Ozav customer was created from their saved
// profile was asked only for the statement, and Ozav was left listing the
// proof of address as still required.
func TestAdapterAsksForTheDocumentsEveryIndividualOwesWithTheApplication(t *testing.T) {
	require.Equal(t, []string{"proof_of_address", "bank_statement"}, ramp.ApplicationDocumentsOf(&Adapter{}))
}

// 23/10 customer 74: Ozav rejected a proof of address ("document_invalid") and
// asked for another. The customer is not rejected, so nothing named why; the
// categories carry the reason to the app while the row stays incomplete.
func TestGetKycNamesWhyAFileWasAskedForAgain(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"in_review","verification":{"mode":"reliance"},
			"requirements":{"due":[],"missing_fields":[],"owner":"integrator","documents_due":[
				{"key":"proof_of_address","doc_type":"proof_of_address","status":"due","reason_code":"document_invalid"},
				{"key":"bank_statement","doc_type":"bank_statement","status":"due","reason_code":"document_invalid"},
				{"key":"government_id_front","doc_type":"identity_document","status":"due","reason_code":"document_invalid"},
				{"key":"tax_return","doc_type":"source_of_funds","status":"due","reason_code":"capacity_unproven"}]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})

	require.NoError(t, err)
	require.Equal(t, models.KycStatusIncomplete, state.Status)
	require.Equal(t, []string{
		models.KycRejectionDocumentUnreadable,
		models.KycRejectionProofOfAddressInvalid,
		models.KycRejectionBankStatementInvalid,
	}, state.RejectionCategories, "in the order the app lists them, and nothing for a limit increase")
	require.Equal(t, []string{"proof_of_address", "bank_statement", "identity_document", "source_of_funds"}, state.Requirements)
}

// Once the file is resubmitted (or accepted) the reason is gone with it.
func TestGetKycDropsTheReasonOnceTheFileIsBackWithOzav(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"in_review","verification":{"mode":"reliance"},
			"requirements":{"due":[],"missing_fields":[],"owner":"ozav","documents_due":[
				{"key":"proof_of_address","doc_type":"proof_of_address","status":"submitted","reason_code":"document_invalid"}]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})

	require.NoError(t, err)
	require.Empty(t, state.RejectionCategories)
}

// A rejected customer keeps its single final category: the document reasons
// are for a customer still being asked, not for one Ozav closed.
func TestGetKycKeepsARejectionFinalWhateverFilesWereAskedFor(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(got *capture, w http.ResponseWriter) {
		if strings.HasSuffix(got.path, "/documents") {
			_, _ = w.Write([]byte(`{"data":[],"still_required":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ozav_cus_1","status":"rejected","decision":{"status":"rejected","reason_code":"document_invalid"},
			"verification":{"mode":"reliance"},"requirements":{"documents_due":[
				{"key":"proof_of_address","doc_type":"proof_of_address","status":"due","reason_code":"document_invalid"}]}}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"})

	require.NoError(t, err)
	require.Equal(t, []string{models.KycRejectionNotEligible}, state.RejectionCategories)
}
