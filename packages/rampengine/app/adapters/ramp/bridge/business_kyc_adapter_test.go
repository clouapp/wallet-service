package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// brazilianCompany is an LTDA with one owner who registers it and one who
// does not, both CPF holders living in Brazil.
func brazilianCompany() ramp.Application {
	address := ramp.Address{StreetLine1: "Avenida Paulista", Number: "1000", StreetLine2: "Sala 12",
		Neighborhood: "Bela Vista", City: "São Paulo", Subdivision: "SP", PostalCode: "01310100", Country: "BRA"}
	return ramp.Application{
		Type:              models.CustomerTypeBusiness,
		SignedAgreementID: "agr_1",
		// What a client asks is ignored: a company's endorsements follow its
		// own identity.
		Endorsements: []string{"pix"},
		Business: &ramp.BusinessProfile{
			TaxIdentity: ramp.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"},
			TaxID:       "12ABC34501DE35", LegalName: "ACME TECNOLOGIA LTDA", TradeName: "ACME",
			CompanyType: models.CompanyTypeLTDA, IncorporationDate: "2019-03-14",
			Email: "financeiro@acme.com.br", Phone: "+5511987654321", Website: "https://acme.com.br",
			Industry: "software_technology", Description: "Desenvolvimento de software sob encomenda",
			RegisteredAddress: address,
			AccountPurpose:    "receive_from_customers", SourceOfFunds: "sales",
			AnnualRevenueBand: "100000_999999", MonthlyVolumeBand: "10000_49999",
			OwnershipAttestedAt: "2026-10-18T12:00:00Z",
		},
		AssociatedPersons: []ramp.AssociatedPerson{
			{
				LocalRef: "ref-ana", Representative: true,
				Roles:            []string{ramp.AssociatedPersonOwner, ramp.AssociatedPersonControl, ramp.AssociatedPersonSigner},
				OwnershipPercent: 60.5, Title: "Sócia-administradora",
				Profile: ramp.Profile{
					FirstName: "Ana", LastName: "Ribeiro", Email: "ana@exemplo.com", Phone: "+5511912345678",
					BirthDate: "1988-05-20", TaxID: "39053344705", TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA",
					Address: ramp.Address{StreetLine1: "Rua Augusta", Number: "500", City: "São Paulo",
						Subdivision: "SP", PostalCode: "01304000", Country: "BRA"},
				},
			},
			{
				LocalRef: "ref-bruno", Roles: []string{ramp.AssociatedPersonOwner}, OwnershipPercent: 39.5,
				Profile: ramp.Profile{
					FirstName: "Bruno", LastName: "Costa", Email: "Bruno@Exemplo.com", Phone: "+5511923456789",
					BirthDate: "1990-01-02", TaxID: "52998224725", TaxIDType: models.TaxIDTypeCPF, TaxIDCountry: "BRA",
					Address: ramp.Address{StreetLine1: "Rua Vergueiro", City: "São Paulo", Subdivision: "SP",
						PostalCode: "04101000", Country: "BRA"},
				},
			},
		},
		IdempotencyKey: "kyc-app:1000000000001:bridge",
	}
}

func readBusinessFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/business_created.json")
	require.NoError(t, err)
	return string(raw)
}

// createdWithPeople is the sandbox's answer to a create (business_created.json)
// listing the two people of brazilianCompany.
func createdWithPeople(t *testing.T) string {
	t.Helper()
	var customer map[string]any
	require.NoError(t, json.Unmarshal([]byte(readBusinessFixture(t)), &customer))
	customer["associated_persons"] = []map[string]string{
		{"id": "ap_ana", "email": "ana@exemplo.com"}, {"id": "ap_bruno", "email": "bruno@exemplo.com"},
	}
	body, err := json.Marshal(customer)
	require.NoError(t, err)
	return string(body)
}

// The create is the company's data only (KYB spec §8, sandbox 18/10/2026):
// Bridge's words for XIP's keys, the CNPJ in its canonical spelling, the
// registered address as the physical one too — the street with its number,
// the complement with the neighbourhood, as a proof of address writes them —
// every person inline with their flags, identity and address written the
// same way, the representative's attestation, and the endorsements the
// company's identity opens.
func TestSubmitBusinessApplicationSendsTheCompany(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{"POST /customers": createdWithPeople(t)})

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, brazilianCompany())

	require.NoError(t, err)
	require.Len(t, *calls, 1)
	sent := (*calls)[0]
	require.Equal(t, "kyc-app:1000000000001:bridge", sent.header.Get("Idempotency-Key"))
	address := map[string]any{"street_line_1": "Avenida Paulista, 1000", "street_line_2": "Sala 12 - Bela Vista",
		"city": "São Paulo", "subdivision": "SP", "postal_code": "01310100", "country": "BRA"}
	require.Equal(t, map[string]any{
		"type":                          "business",
		"business_legal_name":           "ACME TECNOLOGIA LTDA",
		"business_trade_name":           "ACME",
		"business_type":                 "llc",
		"business_description":          "Desenvolvimento de software sob encomenda",
		"business_industry":             []any{"541511"},
		"email":                         "financeiro@acme.com.br",
		"phone":                         "+5511987654321",
		"primary_website":               "https://acme.com.br",
		"incorporation_date":            "2019-03-14",
		"is_dao":                        false,
		"registered_address":            address,
		"physical_address":              address,
		"identifying_information":       []any{map[string]any{"type": "cnpj", "issuing_country": "bra", "number": "12ABC34501DE35"}},
		"account_purpose":               "receive_payments_for_goods_and_services",
		"source_of_funds":               "sales_of_goods_and_services",
		"estimated_annual_revenue_usd":  "100000_999999",
		"expected_monthly_payments_usd": float64(49999),
		"conducts_money_services":       false,
		"signed_agreement_id":           "agr_1",
		"endorsements":                  []any{"pix_onramp", "pix_offramp"},
		"associated_persons": []any{
			map[string]any{
				"first_name": "Ana", "last_name": "Ribeiro", "email": "ana@exemplo.com", "phone": "+5511912345678",
				"birth_date": "1988-05-20",
				"residential_address": map[string]any{"street_line_1": "Rua Augusta, 500", "city": "São Paulo",
					"subdivision": "SP", "postal_code": "01304000", "country": "BRA"},
				"identifying_information": []any{map[string]any{"type": "cpf", "issuing_country": "bra", "number": "39053344705"}},
				"has_ownership":           true, "has_control": true, "is_signer": true, "is_director": false,
				"ownership_percentage": float64(60), "title": "Sócia-administradora",
				"attested_ownership_structure_at": "2026-10-18T12:00:00Z",
			},
			map[string]any{
				"first_name": "Bruno", "last_name": "Costa", "email": "Bruno@Exemplo.com", "phone": "+5511923456789",
				"birth_date": "1990-01-02",
				"residential_address": map[string]any{"street_line_1": "Rua Vergueiro", "city": "São Paulo",
					"subdivision": "SP", "postal_code": "04101000", "country": "BRA"},
				"identifying_information": []any{map[string]any{"type": "cpf", "issuing_country": "bra", "number": "52998224725"}},
				"has_ownership":           true, "has_control": false, "is_signer": false, "is_director": false,
				"ownership_percentage": float64(39),
			},
		},
	}, sent.body)

	// Bridge answers each person by id and e-mail; the state keeps the ids by
	// the app's references, for the documents and the next resubmission.
	require.Equal(t, "cus_business_1", state.ExternalCustomerID)
	require.Equal(t, map[string]string{"ref-ana": "ap_ana", "ref-bruno": "ap_bruno"}, state.AssociatedPersonIDs)
	require.Equal(t, "agr_1", state.SignedAgreementID)
	// A business is born awaiting_ubo until each partner's photographed
	// document arrives.
	require.Equal(t, models.KycStatusRequiresInfo, state.Status)
	require.Contains(t, state.Requirements, "government_id_document")
}

// A company from abroad asks the deposit only, its EIN in Bridge's dashed
// spelling, its US subdivision as is; it operates where it says it does.
func TestSubmitBusinessApplicationFromAbroad(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{"POST /customers": createdWithPeople(t)})
	app := brazilianCompany()
	app.Business.TaxIdentity = ramp.TaxIdentity{Type: models.TaxIDTypeEIN, Country: "USA"}
	app.Business.TaxID = "123456789"
	app.Business.CompanyType = models.CompanyTypeLLC
	app.Business.RegisteredAddress = ramp.Address{StreetLine1: "1450 Brickell Ave", City: "Miami", Subdivision: "FL",
		PostalCode: "33131", Country: "USA"}
	physical := ramp.Address{StreetLine1: "200 Biscayne Blvd Way", City: "Miami", Subdivision: "FL", PostalCode: "33131", Country: "USA"}
	app.Business.PhysicalAddress = &physical
	app.Business.Website = ""

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)

	require.NoError(t, err)
	body := (*calls)[0].body
	require.Equal(t, []any{"pix_onramp"}, body["endorsements"])
	require.Equal(t, []any{map[string]any{"type": "ein", "issuing_country": "usa", "number": "12-3456789"}}, body["identifying_information"])
	require.Equal(t, "llc", body["business_type"])
	require.Equal(t, "200 Biscayne Blvd Way", body["physical_address"].(map[string]any)["street_line_1"],
		"a street with no number declared is the street")
	require.Equal(t, "FL", body["registered_address"].(map[string]any)["subdivision"])
	require.NotContains(t, body, "primary_website", "a company with no website says none")
}

// A company that moves its clients' money says how, and Bridge wants it twice:
// what the services are and how the company screens them. A purpose of
// "other" carries its text.
func TestSubmitBusinessApplicationExplainsMoneyServicesAndAnOtherPurpose(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{"POST /customers": createdWithPeople(t)})
	app := brazilianCompany()
	app.Business.ConductsMoneyServices = true
	app.Business.MoneyServicesDescription = "Recebe pagamentos em nome de lojistas parceiros"
	app.Business.AccountPurpose = "other"
	app.Business.AccountPurposeOther = "Pagar fretes internacionais"

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)

	require.NoError(t, err)
	body := (*calls)[0].body
	require.Equal(t, true, body["conducts_money_services"])
	require.Equal(t, "Recebe pagamentos em nome de lojistas parceiros", body["conducts_money_services_description"])
	require.Equal(t, "Recebe pagamentos em nome de lojistas parceiros", body["compliance_screening_explanation"])
	require.Equal(t, "other", body["account_purpose"])
	require.Equal(t, "Pagar fretes internacionais", body["account_purpose_other"])
}

// Bridge refusing a company's e-mail as another customer's is never adopted
// (KYB spec §8): the e-mail of a company proves nothing about whose customer
// that is. The user changes the e-mail.
func TestSubmitBusinessApplicationNeverAdoptsACustomerByEmail(t *testing.T) {
	// The sandbox's own answer to a second company under one e-mail
	// (18/10/2026).
	adapter, calls := answeringAdapter(t, http.StatusBadRequest, `{"code":"invalid_parameters","message":"Please resubmit the following parameters that are either missing or invalid","source":{"location":"body","key":{"email":"A customer with this email already exists"}}}`)

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, brazilianCompany())

	require.ErrorIs(t, err, ramp.ErrEmailInUse)
	require.Equal(t, []string{"POST /customers"}, *calls, "no lookup by e-mail, no update")
}

func TestSubmitBusinessApplicationRequiresTheTerms(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})

	app := brazilianCompany()
	app.SignedAgreementID = ""
	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)
	require.ErrorIs(t, err, ramp.ErrTosRequired)
	require.Empty(t, *calls)
}

// The company is what makes an application a business's, never the type a
// client sends: a person's body declaring type business goes the person's way,
// as it went before the KYB (the PF request rule closes the type to
// individual).
func TestAPersonsApplicationTypedBusinessStaysAPersons(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{"POST /customers": `{"id":"cus_1","status":"incomplete"}`})
	app := fullApplication()
	app.Type = models.CustomerTypeBusiness

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)

	require.NoError(t, err)
	body := (*calls)[0].body
	require.Equal(t, app.Profile.FirstName, body["first_name"])
	require.NotContains(t, body, "business_legal_name")
	require.NotContains(t, body, "associated_persons")
}

// A resubmission updates the company without restating its people, then
// updates each person Bridge knows, creates each new one and removes the one
// the application no longer lists (API contract §5.2). The state is read back
// once everything landed.
func TestResubmitBusinessApplicationReplacesItsPeople(t *testing.T) {
	var customer map[string]any
	require.NoError(t, json.Unmarshal([]byte(readBusinessFixture(t)), &customer))
	customer["associated_persons"] = []map[string]string{
		{"id": "ap_ana", "email": "ana@exemplo.com"}, {"id": "ap_gone", "email": "gone@exemplo.com"},
	}
	current, err := json.Marshal(customer)
	require.NoError(t, err)
	adapter, calls := routedAdapter(t, map[string]string{
		"PUT /customers/cus_business_1":                               string(current),
		"PUT /customers/cus_business_1/associated_persons/ap_ana":     `{"id":"ap_ana"}`,
		"POST /customers/cus_business_1/associated_persons":           `{"id":"ap_bruno"}`,
		"DELETE /customers/cus_business_1/associated_persons/ap_gone": `{}`,
		"GET /customers/cus_business_1":                               readBusinessFixture(t),
	})
	app := brazilianCompany()
	app.AssociatedPersons[0].ExternalID = "ap_ana"

	state, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, app)

	require.NoError(t, err)
	var sequence []string
	for _, c := range *calls {
		sequence = append(sequence, c.method+" "+c.path)
	}
	require.Equal(t, []string{
		"PUT /customers/cus_business_1",
		"PUT /customers/cus_business_1/associated_persons/ap_ana",
		"POST /customers/cus_business_1/associated_persons",
		"DELETE /customers/cus_business_1/associated_persons/ap_gone",
		"GET /customers/cus_business_1",
	}, sequence)
	require.NotContains(t, (*calls)[0].body, "associated_persons")
	require.Equal(t, "ACME TECNOLOGIA LTDA", (*calls)[0].body["business_legal_name"])
	require.Equal(t, "Ana", (*calls)[1].body["first_name"])
	require.Equal(t, "Bruno", (*calls)[2].body["first_name"])
	require.Empty(t, (*calls)[1].header.Get("Idempotency-Key"), "Bridge refuses a key on a PUT")
	require.Equal(t, map[string]string{"ref-ana": "ap_ana", "ref-bruno": "ap_bruno"}, state.AssociatedPersonIDs)
	require.Equal(t, "agr_1", state.SignedAgreementID)
}

// The documents go after the application, by PUT (KYB spec §8, §18.3): each
// person's on the person, by the id the application's state kept, and then
// the company's by Bridge's purposes on the customer, whose answer is the
// state with everything in.
func TestSubmitBusinessDocuments(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"PUT /customers/cus_business_1/associated_persons/ap_ana": `{"id":"ap_ana"}`,
		"PUT /customers/cus_business_1":                           readBusinessFixture(t),
	})
	docs := ramp.Documents{
		CompanyDocuments: []ramp.CompanyDocument{
			{Purpose: ramp.CompanyDocumentFormation, File: "data:application/pdf;base64,AA"},
			{Purpose: ramp.CompanyDocumentProofOfAddress, File: "data:image/jpeg;base64,AB"},
			{Purpose: ramp.CompanyDocumentSourceOfFunds, File: "data:application/pdf;base64,AC"},
			{Purpose: ramp.CompanyDocumentTaxIdentification, File: "data:application/pdf;base64,AD"},
			{Purpose: ramp.CompanyDocumentNatureOfBusiness, File: "data:application/pdf;base64,AE"},
		},
		PersonDocuments: []ramp.PersonDocuments{{
			LocalRef: "ref-ana", ExternalID: "ap_ana",
			Documents: []ramp.IdentifyingDocument{
				{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"},
				{Type: "national_id", IssuingCountry: "BRA", ImageFront: "data:image/jpeg;base64,F", ImageBack: "data:image/jpeg;base64,B"},
			},
		}},
	}

	state, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, docs)

	require.NoError(t, err)
	require.Len(t, *calls, 2)
	require.Equal(t, "/customers/cus_business_1/associated_persons/ap_ana", (*calls)[0].path)
	require.Equal(t, map[string]any{"identifying_information": []any{
		map[string]any{"type": "cpf", "issuing_country": "bra", "number": "39053344705"},
		map[string]any{"type": "national_id", "issuing_country": "bra", "image_front": "data:image/jpeg;base64,F", "image_back": "data:image/jpeg;base64,B"},
	}}, (*calls)[0].body)
	require.Equal(t, "/customers/cus_business_1", (*calls)[1].path)
	require.Equal(t, map[string]any{"documents": []any{
		map[string]any{"purposes": []any{"business_formation"}, "file": "data:application/pdf;base64,AA"},
		map[string]any{"purposes": []any{"proof_of_address"}, "file": "data:image/jpeg;base64,AB"},
		map[string]any{"purposes": []any{"proof_of_source_of_funds"}, "file": "data:application/pdf;base64,AC"},
		map[string]any{"purposes": []any{"proof_of_tax_identification"}, "file": "data:application/pdf;base64,AD"},
		map[string]any{"purposes": []any{"proof_of_nature_of_business"}, "file": "data:application/pdf;base64,AE"},
	}}, (*calls)[1].body)
	require.Equal(t, "cus_business_1", state.ExternalCustomerID)
}

// Only the people's documents: the state is read back once they landed.
func TestSubmitBusinessPersonDocumentsAlone(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{
		"PUT /customers/cus_business_1/associated_persons/ap_ana": `{"id":"ap_ana"}`,
		"GET /customers/cus_business_1":                           readBusinessFixture(t),
	})

	state, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, ramp.Documents{
		PersonDocuments: []ramp.PersonDocuments{{LocalRef: "ref-ana", ExternalID: "ap_ana",
			Documents: []ramp.IdentifyingDocument{{Type: "passport", IssuingCountry: "ARG", ImageFront: "data:image/jpeg;base64,F"}}}},
	})

	require.NoError(t, err)
	require.Len(t, *calls, 2)
	require.Equal(t, http.MethodGet, (*calls)[1].method)
	require.Equal(t, "cus_business_1", state.ExternalCustomerID)
}

// A person's document with no provider id to place it on fails before any
// call: sending the company's documents and losing the person's would leave
// the business awaiting_ubo with nothing to say why.
func TestSubmitBusinessDocumentsRefusesAPersonWithoutAnID(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{})
	docs := ramp.Documents{
		CompanyDocuments: []ramp.CompanyDocument{{Purpose: ramp.CompanyDocumentFormation, File: "data:application/pdf;base64,AA"}},
		PersonDocuments:  []ramp.PersonDocuments{{LocalRef: "ref-ana", Documents: []ramp.IdentifyingDocument{{Type: "passport", ImageFront: "data:image/jpeg;base64,F"}}}},
	}

	_, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, docs)

	require.Error(t, err)
	require.Empty(t, *calls)

	_, err = adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"},
		ramp.Documents{CompanyDocuments: []ramp.CompanyDocument{{Purpose: "selfie", File: "data:image/jpeg;base64,F"}}})
	require.Error(t, err, "a purpose Bridge has no word for is refused, not dropped")
	require.Empty(t, *calls)
}

// A person Bridge is still processing refuses the update with
// resource_state_conflict, as a customer does: a refusal of the timing, which
// the worker retries.
func TestSubmitBusinessDocumentsRetriesAPersonStillBeingProcessed(t *testing.T) {
	adapter, calls := answeringAdapter(t, http.StatusConflict, `{"code":"resource_state_conflict","message":"busy"}`)

	_, err := adapter.SubmitDocuments(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, ramp.Documents{
		CompanyDocuments: []ramp.CompanyDocument{{Purpose: ramp.CompanyDocumentFormation, File: "data:application/pdf;base64,AA"}},
		PersonDocuments: []ramp.PersonDocuments{{LocalRef: "ref-ana", ExternalID: "ap_ana",
			Documents: []ramp.IdentifyingDocument{{Type: "passport", IssuingCountry: "ARG", ImageFront: "data:image/jpeg;base64,F"}}}},
	})

	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
	// The company's documents had not gone yet, so the retry uploads them
	// once.
	require.Equal(t, []string{"PUT /customers/cus_business_1/associated_persons/ap_ana"}, *calls)
}

// The hosted page of a business, the fallback when the API refuses it, is
// asked as one, named by its legal name.
func TestHostedLinkForABusiness(t *testing.T) {
	adapter, calls := routedAdapter(t, map[string]string{"POST /kyc_links": `{"id":"kl_1","kyc_link":"https://bridge.example/kyc"}`})

	_, err := adapter.CreateHostedKycLink(context.Background(), ramp.CustomerRef{}, ramp.HostedLinkOpts{
		FullName: "ACME TECNOLOGIA LTDA", Email: "financeiro@acme.com.br", Type: models.CustomerTypeBusiness,
	})

	require.NoError(t, err)
	require.Equal(t, "business", (*calls)[0].body["type"])
	require.Equal(t, "ACME TECNOLOGIA LTDA", (*calls)[0].body["full_name"])
}

// answeringAdapter answers every call with status and body, recording the
// calls made.
func answeringAdapter(t *testing.T, status int, body string) (*Adapter, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil), &calls
}

// A business customer's endorsements list objects among their pending
// requirements ({"associated_person": id, "items": [...]}, sandbox
// 18/10/2026). A list read as strings failed the whole decode, so no read,
// refresh or webhook of a business could land.
func TestABusinessCustomerDecodes(t *testing.T) {
	adapter, _ := routedAdapter(t, map[string]string{"GET /customers/cus_business_1": readBusinessFixture(t)})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"})

	require.NoError(t, err)
	require.Equal(t, models.KycStatusRequiresInfo, state.Status)
	require.Contains(t, state.Requirements, "government_id_document")
	require.Contains(t, state.Requirements, "business_formation_document")
	require.Contains(t, state.Requirements, "business_website_verification", "the pending strings are still read")
}

// A resubmission that moves the company to an e-mail another customer holds
// is the same refusal as a create's.
func TestResubmitBusinessApplicationToATakenEmail(t *testing.T) {
	adapter, calls := answeringAdapter(t, http.StatusBadRequest, `{"code":"invalid_parameters","message":"Please resubmit","source":{"location":"body","key":{"email":"A customer with this email already exists"}}}`)

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{ExternalID: "cus_business_1"}, brazilianCompany())

	require.ErrorIs(t, err, ramp.ErrEmailInUse)
	require.Equal(t, []string{"PUT /customers/cus_business_1"}, *calls)
}
