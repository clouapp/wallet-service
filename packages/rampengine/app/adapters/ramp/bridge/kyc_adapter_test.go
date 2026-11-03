package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestDashedUSTaxID(t *testing.T) {
	require.Equal(t, "287-65-4321", dashedUSTaxID("287654321"))
	require.Equal(t, "287-65-4321", dashedUSTaxID("287-65-4321"))
	require.Equal(t, "912-78-1234", dashedUSTaxID(" 912 78 1234 "))
	require.Equal(t, "abc", dashedUSTaxID(" abc "), "not nine digits: returned trimmed")
}

// capture records what the adapter actually put on the wire, which is the only
// thing these tests are about: the adapter is a mapper, so the assertions are
// on the request it builds and the struct it returns.
type capture struct {
	method string
	path   string
	query  string
	body   map[string]any
	// raw is the body as sent, for the assertions about bytes rather than
	// fields (a payload that must not change at all).
	raw    []byte
	header http.Header
}

func newTestAdapter(t *testing.T, handler func(*capture, http.ResponseWriter)) (*Adapter, *capture) {
	t.Helper()
	got := &capture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query, got.header = r.Method, r.URL.Path, r.URL.RawQuery, r.Header
		got.raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(got.raw, &got.body)
		w.Header().Set("Content-Type", "application/json")
		handler(got, w)
	}))
	t.Cleanup(server.Close)

	client := NewClient(Config{BaseURL: server.URL, APIKey: "sk_test", Timeout: 2 * time.Second})
	return New(func(context.Context) (*Client, error) { return client, nil }, nil), got
}

// unreachableAdapter fails the test if any HTTP call happens. Guard rejections
// have to be provably local — a wasted upstream call on a doomed request is
// what the guard exists to prevent.
func unreachableAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(func(context.Context) (*Client, error) {
		t.Fatal("adapter must reject locally, without calling Bridge")
		return nil, nil
	}, nil)
}

func fullApplication() ramp.Application {
	return ramp.Application{
		Type:              "individual",
		SignedAgreementID: "sa_123",
		Endorsements:      []string{models.EndorsementPix},
		Profile: ramp.Profile{
			FirstName: "Luiza", LastName: "Souza",
			Email: "luiza@example.com", Phone: "+5511999999999",
			BirthDate: "1990-04-02",
			Address: ramp.Address{
				StreetLine1: "Rua A, 100", City: "Sao Paulo",
				Subdivision: "SP", PostalCode: "01000-000", Country: "BRA",
			},
		},
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"},
			{Type: "national_id", IssuingCountry: "BRA",
				ImageFront: "data:image/jpeg;base64,AAAA", ImageBack: "data:image/jpeg;base64,BBBB"},
		},
		LivenessCheckSelfies: []ramp.Selfie{{Image: "data:image/jpeg;base64,CCCC"}},
		IdempotencyKey:       "app-key-1",
	}
}

// A person's address keeps the lines Bridge has always received from XIP: the
// number and the neighbourhood the app collects are not added to them (the
// business address carries them; whether the person's should is Luiz's to
// decide).
func TestSubmitApplicationKeepsThePersonsAddressLines(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review","endorsements":[]}`))
	})
	app := fullApplication()
	app.Profile.Address = ramp.Address{StreetLine1: "Rua Augusta", Number: "500", StreetLine2: "Apto 12",
		Neighborhood: "Consolação", City: "São Paulo", Subdivision: "SP", PostalCode: "01304000", Country: "BRA"}

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{}, app)

	require.NoError(t, err)
	require.Equal(t, map[string]any{"street_line_1": "Rua Augusta", "street_line_2": "Apto 12", "city": "São Paulo",
		"subdivision": "SP", "postal_code": "01304000", "country": "BRA"}, got.body["residential_address"])
}

func TestSubmitApplicationCreatesCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review",
			"endorsements":[{"name":"pix","status":"incomplete"}],
			"has_accepted_terms_of_service":true}`))
	})

	state, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers", got.path, "no external id yet: the customer is born here")
	require.Equal(t, "app-key-1", got.header.Get("Idempotency-Key"))

	require.Equal(t, "individual", got.body["type"])
	require.Equal(t, "Luiza", got.body["first_name"])
	require.Equal(t, "sa_123", got.body["signed_agreement_id"])
	require.Equal(t, []any{"pix", "base"}, got.body["endorsements"])
	require.Equal(t, "1990-04-02", got.body["birth_date"])

	address, ok := got.body["residential_address"].(map[string]any)
	require.True(t, ok, "address must be nested under residential_address")
	require.Equal(t, "BRA", address["country"])
	require.Equal(t, "01000-000", address["postal_code"])

	docs, ok := got.body["identifying_information"].([]any)
	require.True(t, ok)
	require.Len(t, docs, 2)
	require.Equal(t, "39053344705", docs[0].(map[string]any)["number"])
	require.Equal(t, "data:image/jpeg;base64,AAAA", docs[1].(map[string]any)["image_front"])
	// Objects, not bare strings: verified against the sandbox, which rejects
	// the flat form.
	require.Equal(t, []any{map[string]any{"image": "data:image/jpeg;base64,CCCC"}},
		got.body["liveness_check_selfies"])

	require.Equal(t, "cus_1", state.ExternalCustomerID)
	require.Equal(t, models.KycStatusUnderReview, state.Status)
	require.Equal(t, models.TosStatusApproved, state.TosStatus)
	require.Equal(t, "sa_123", state.SignedAgreementID, "the agreement we submitted is echoed back for persistence")
	require.Len(t, state.Endorsements, 1)
	require.Equal(t, models.EndorsementStatusIncomplete, state.Endorsements[0].Status)
	require.NotEmpty(t, state.Raw)
}

func TestSubmitApplicationRecoversDuplicateEmail(t *testing.T) {
	calls := 0
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		calls++
		switch calls {
		case 1:
			require.Equal(t, http.MethodPost, c.method)
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"email already exists"}`))
		case 2:
			require.Equal(t, http.MethodGet, c.method)
			require.Equal(t, "/customers", c.path)
			require.Equal(t, "email=luiza%40example.com", c.query)
			_, _ = w.Write([]byte(`{"data":[{"id":"cus_existing","email":"luiza@example.com","status":"active"}]}`))
		default:
			require.Equal(t, http.MethodPut, c.method)
			require.Equal(t, "/customers/cus_existing", c.path)
			_, _ = w.Write([]byte(`{"id":"cus_existing","status":"active"}`))
		}
	})

	state, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, "cus_existing", state.ExternalCustomerID)
	require.Equal(t, http.MethodPut, got.method)
}

// The address is a query parameter and must stay out of Request.Path: Path is
// what LogUpstreamCall writes at Info on every call and what UpstreamError
// copies into its message.
func TestCustomerLookupKeepsTheEmailOutOfThePath(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters"}`))
	})

	_, err := adapter.findCustomerIDByEmail(context.Background(), "luiza@example.com")

	require.Error(t, err)
	require.Equal(t, "/customers", got.path)
	require.Equal(t, "email=luiza%40example.com", got.query)

	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "/customers", upstreamErr.Path)
	require.NotContains(t, err.Error(), "luiza")
}

func TestSubmitApplicationDoesNotRecoverOnEmailMismatch(t *testing.T) {
	calls := 0
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		calls++
		switch calls {
		case 1:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"email already exists"}`))
		case 2:
			require.Equal(t, http.MethodGet, c.method)
			_, _ = w.Write([]byte(`{"data":[{"id":"cus_other","email":"other@example.com","status":"active"}]}`))
		default:
			t.Fatal("email mismatch must not trigger customer update")
		}
	})

	_, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.Error(t, err)
	require.Equal(t, 2, calls, "email mismatch must not trigger customer update")
}

func TestSubmitApplicationDoesNotRecoverOnInvalidTaxId(t *testing.T) {
	calls := 0
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		calls++
		require.Equal(t, 1, calls, "invalid tax_id must not trigger email lookup")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"tax_id 39053344705 is invalid"}`))
	})

	_, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid_parameters")
	require.NotContains(t, err.Error(), "duplicate_email")
}

func TestSubmitApplicationUpdatesExistingCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"active"}`))
	})

	state, err := adapter.SubmitApplication(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, fullApplication())
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/customers/cus_1", got.path, "resubmission updates, it never creates a second customer")
	require.Equal(t, models.KycStatusApproved, state.Status)
}

// Bridge rejects a customer created without a signed agreement. Catching it
// here spares the user a round-trip and keeps the upstream error out of a flow
// we can describe better ourselves.
func TestSubmitApplicationRequiresSignedAgreement(t *testing.T) {
	app := fullApplication()
	app.SignedAgreementID = ""

	_, err := unreachableAdapter(t).SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.ErrorIs(t, err, ramp.ErrTosRequired)
}

func TestSubmitDocumentsRequiresExistingCustomer(t *testing.T) {
	_, err := unreachableAdapter(t).SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7}, ramp.Documents{LivenessCheckSelfies: []ramp.Selfie{{Image: "data:x"}}})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// A document resubmission must not restate the profile: Bridge treats a PUT as
// a patch, and echoing stale name/address fields would overwrite corrections
// the user made through another channel.
func TestSubmitDocumentsSendsOnlyDocumentFields(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{
			IdentifyingInformation: []ramp.IdentifyingDocument{{Type: "national_id", ImageFront: "data:front"}},
			LivenessCheckSelfies:   []ramp.Selfie{{Image: "data:selfie"}},
			IdempotencyKey:         "docs-key-1",
		})
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/customers/cus_1", got.path)
	require.Contains(t, got.body, "identifying_information")
	require.Contains(t, got.body, "liveness_check_selfies")
	require.NotContains(t, got.body, "first_name")
	require.NotContains(t, got.body, "residential_address")
	require.NotContains(t, got.body, "endorsements")
}

// Bridge takes a proof of address on the customer's `documents` array, each
// entry naming its purposes and carrying the file as a data URI (doc read
// 04/09: .pdf .jpeg .jpg .png .heic .tif, min 200x200, max 24 MB).
func TestSubmitDocumentsSendsAProofOfAddressUnderDocuments(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{ProofOfAddress: "data:image/jpeg;base64,AAAA"})
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, got.method)
	documents, ok := got.body["documents"].([]any)
	require.True(t, ok, "documents must be a list")
	require.Len(t, documents, 1)
	entry := documents[0].(map[string]any)
	require.Equal(t, []any{"proof_of_address"}, entry["purposes"])
	require.Equal(t, "data:image/jpeg;base64,AAAA", entry["file"])
	require.NotContains(t, got.body, "identifying_information")
}

func TestSubmitDocumentsOmitsDocumentsWithoutAProofOfAddress(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})
	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{LivenessCheckSelfies: []ramp.Selfie{{Image: "data:selfie"}}})
	require.NoError(t, err)
	require.NotContains(t, got.body, "documents")
}

// Bridge does not take a bank statement — it is Ozav's — and the fan-out can
// still reach Bridge with a submission that carries nothing else. A PUT with
// an empty body would be a request that changes nothing and that Bridge may
// read as a resubmission, so the adapter answers with a read instead.
func TestSubmitDocumentsCarryingOnlyABankStatementOnlyReads(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"active"}`))
	})

	state, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{
			IdentifyingInformation: []ramp.IdentifyingDocument{{}},
			BankStatement:          "data:application/pdf;base64,JVBERi0=",
		})
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, "/customers/cus_1", got.path)
	require.Equal(t, "cus_1", state.ExternalCustomerID)
}

// The KYC service always builds one IdentifyingDocument entry, even for a
// proof-of-address-only submission — it just leaves every field empty (see
// documentsFrom in app/services/client/kyc). Bridge must not be told about that
// entry: an empty object in identifying_information names no document and is
// at best noise on a PUT that is otherwise a clean patch.
//
// The empty entry is spelled out here (IdentifyingInformation:
// []ramp.IdentifyingDocument{{}}) rather than left as the nil the zero value
// would give: nil already produces no identifying_information with today's
// code (len(docs) == 0 short-circuits), so a struct literal that omitted the
// field would pass before this fix as easily as after it and prove nothing.
func TestSubmitDocumentsOmitsAnEmptyIdentityEntry(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{
			IdentifyingInformation: []ramp.IdentifyingDocument{{}},
			ProofOfAddress:         "data:image/jpeg;base64,AAAA",
		})
	require.NoError(t, err)

	require.Contains(t, got.body, "documents")
	require.NotContains(t, got.body, "identifying_information")
}

// An entry that carries anything at all — here just a front image — is still
// a real document and must still be sent.
func TestSubmitDocumentsKeepsAnIdentityEntryWithAnImage(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{{ImageFront: "data:front"}}})
	require.NoError(t, err)

	docs, ok := got.body["identifying_information"].([]any)
	require.True(t, ok)
	require.Len(t, docs, 1)
	require.Equal(t, "data:front", docs[0].(map[string]any)["image_front"])
}

// Bridge documents the Idempotency-Key header as POST-only — "do not include an
// Idempotency-Key with GET, PUT, PATCH, or DELETE requests" — and answers 422
// when one rides along on a customer update. SubmitApplication already learned
// that on its own PUT branch; the document submission runs against the same
// endpoint and owes the same omission.
func TestSubmitDocumentsOmitsIdempotencyKeyOnCustomerPut(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{
			IdentifyingInformation: []ramp.IdentifyingDocument{{Type: "national_id", ImageFront: "data:front"}},
			IdempotencyKey:         "kyc-docs:abc123",
		})
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, got.method)
	require.Empty(t, got.header.Get("Idempotency-Key"))
}

// Measured in production on 01/10/2026: documents sent five seconds after the
// POST that created the customer were refused with resource_state_conflict
// while Bridge was still running its checks on it, and the same customer took
// a PUT again minutes later. The refusal is about when, not about what was
// sent, so the caller is told to send the same request again.
func TestSubmitDocumentsReadsACustomerStateConflictAsRetryable(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"resource_state_conflict","message":"customer is being processed"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{LivenessCheckSelfies: []ramp.Selfie{{Image: "data:selfie"}}})
	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
}

// Only the state conflict: a refusal of what was sent is the same refusal on
// every attempt, and reading it as retryable would upload the same bad image
// until the staging expired.
func TestSubmitDocumentsDoesNotReadARefusalOfTheBodyAsRetryable(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_parameters","message":"image is too small"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{LivenessCheckSelfies: []ramp.Selfie{{Image: "data:selfie"}}})
	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrRetryableUpstream)
}

func TestGetKycMapsStatusEndorsementsAndReasons(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{
			"id":"cus_1","status":"rejected","has_accepted_terms_of_service":true,
			"endorsements":[
				{"name":"pix","status":"revoked","requirements":{"issues":["id_expired"]}},
				{"name":"base","status":"approved"}
			],
			"rejection_reasons":[
				{"reason":"Document unreadable","developer_reason":"vendor_code_88 internal detail"}
			],
			"requirements_due":["proof_of_address"]
		}`))
	})

	state, err := adapter.GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, "/customers/cus_1", got.path)

	require.Equal(t, models.KycStatusRejected, state.Status)
	require.Len(t, state.Endorsements, 2)
	require.Equal(t, models.EndorsementStatusRevoked, state.Endorsements[0].Status)
	require.Equal(t, []string{"id_expired"}, state.Endorsements[0].Requirements)
	require.Equal(t, []string{"proof_of_address", "id_expired"}, state.Requirements)

	// developer_reason is Bridge's internal diagnostic. It is not written for a
	// user and must not be surfaced as one.
	require.Equal(t, []string{"Document unreadable"}, state.RejectionReasons)
}

func TestGetKycRequiresExistingCustomer(t *testing.T) {
	_, err := unreachableAdapter(t).GetKyc(context.Background(), ramp.CustomerRef{CustomerID: 7})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

func TestGetRequirementsDerivesFromCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{
			"id":"cus_1","status":"incomplete",
			"endorsements":[{"name":"pix","status":"incomplete",
				"requirements":{"missing":{"all_of":["tax_id"],"any_of":["passport"]}}}],
			"requirements_due":["proof_of_address"]
		}`))
	})

	req, err := adapter.GetRequirements(context.Background(), ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)

	require.Equal(t, "/customers/cus_1", got.path)
	require.Equal(t, models.KycStatusIncomplete, req.Status)
	require.Equal(t, []string{"proof_of_address", "tax_id", "passport"}, req.Missing)
	require.Len(t, req.Endorsements, 1)
}

func TestRequestEndorsementsPatchesCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review",
			"endorsements":[{"name":"pix_offramp","status":"pending"}]}`))
	})

	state, err := adapter.RequestEndorsements(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}, []string{models.EndorsementPixOfframp})
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/customers/cus_1", got.path)
	require.Equal(t, []any{"pix_offramp"}, got.body["endorsements"])
	require.NotContains(t, got.body, "first_name")
	require.Equal(t, models.EndorsementStatusPending, state.Endorsements[0].Status)
}

func TestCreateTosSession(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"url":"https://dashboard.bridge.xyz/tos?token=abc"}`))
	})

	session, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{CustomerID: 7},
		ramp.TosOpts{RedirectURI: "xip://kyc/tos", IdempotencyKey: "tos-key-1"})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers/tos_links", got.path)
	require.Equal(t, "xip://kyc/tos", got.body["redirect_uri"])
	require.Equal(t, "tos-key-1", got.header.Get("Idempotency-Key"))
	require.Equal(t, "https://dashboard.bridge.xyz/tos?redirect_uri=xip%3A%2F%2Fkyc%2Ftos&token=abc", session.URL)
}

func TestCreateTosSessionPreservesExistingRedirectURI(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"url":"https://compliance.bridge.xyz/tos?redirect_uri=xip%3A%2F%2Fkyc%2Ftos"}`))
	})

	session, err := adapter.CreateTosSession(context.Background(), ramp.CustomerRef{CustomerID: 7},
		ramp.TosOpts{RedirectURI: "xip://kyc/tos", IdempotencyKey: "tos-key-2"})
	require.NoError(t, err)
	require.Equal(t, "https://compliance.bridge.xyz/tos?redirect_uri=xip%3A%2F%2Fkyc%2Ftos", session.URL)
}

// Without a customer yet, the hosted link is what materializes one — which is
// why the service has to hand over name and email here.
func TestCreateHostedKycLinkCreatesCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"kyc_link_1","customer_id":"cus_9",
			"kyc_link":"https://bridge.xyz/kyc/abc","tos_link":"https://bridge.xyz/tos/abc"}`))
	})

	link, err := adapter.CreateHostedKycLink(context.Background(), ramp.CustomerRef{CustomerID: 7},
		ramp.HostedLinkOpts{
			FullName: "Luiza Souza", Email: "luiza@example.com",
			Endorsements: []string{models.EndorsementPix}, RedirectURI: "xip://kyc/done",
			Reason: "api_submit_failed", IdempotencyKey: "link-key-1",
		})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/kyc_links", got.path)
	require.Equal(t, "Luiza Souza", got.body["full_name"])
	require.Equal(t, "individual", got.body["type"])
	require.Equal(t, "link-key-1", got.header.Get("Idempotency-Key"))

	require.Equal(t, "kyc_link_1", link.LinkID)
	require.Equal(t, "cus_9", link.ExternalCustomerID)
	require.Equal(t, "https://bridge.xyz/kyc/abc", link.URL)
	// Bridge prefers API docs, so any hosted link it issues is a fallback.
	require.Equal(t, ramp.CollectionModeHostedFallback, link.CollectionMode)
}

func TestCreateHostedKycLinkForExistingCustomer(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"url":"https://bridge.xyz/kyc/def"}`))
	})

	link, err := adapter.CreateHostedKycLink(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.HostedLinkOpts{Endorsements: []string{models.EndorsementPix}, RedirectURI: "xip://kyc/done"})
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, got.method)
	require.Equal(t, "/customers/cus_1/kyc_link", got.path)
	require.Contains(t, got.query, "endorsement=pix")
	require.Contains(t, got.query, "redirect_uri=xip%3A%2F%2Fkyc%2Fdone")

	require.Equal(t, "https://bridge.xyz/kyc/def", link.URL)
	require.Equal(t, "cus_1", link.ExternalCustomerID, "the ref's customer is echoed so the service can persist unconditionally")
}

// Before any submit there is nothing upstream to ask about: the row is local
// and pending, and Bridge must not be called for it.
func TestStartIntegrationWithoutCustomerIsLocalOnly(t *testing.T) {
	integration, err := unreachableAdapter(t).StartIntegration(context.Background(), ramp.CustomerRef{CustomerID: 7})
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusPending, integration.Status)
	require.Empty(t, integration.ExternalCustomerID)
}

func TestStartIntegrationWithCustomerConfirmsUpstream(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"active"}`))
	})

	integration, err := adapter.RefreshIntegration(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)

	require.Equal(t, "/customers/cus_1", got.path)
	require.Equal(t, models.IntegrationStatusActive, integration.Status)
	require.Equal(t, "cus_1", integration.ExternalCustomerID)
}

func TestOffboardedCustomerDisablesIntegration(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"offboarded"}`))
	})

	integration, err := adapter.RefreshIntegration(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusDisabled, integration.Status)
}

func TestSimulateKycApprovalSendsIdempotencyKey(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
	})

	err := adapter.SimulateKycApproval(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/customers/cus_1/simulate_kyc_approval", got.path)
	require.Equal(t, "simulate-kyc:cus_1", got.header.Get("Idempotency-Key"))
}

func TestSimulateKycApprovalRequiresExistingCustomer(t *testing.T) {
	err := unreachableAdapter(t).SimulateKycApproval(context.Background(), ramp.CustomerRef{CustomerID: 7})
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

func TestAdapterIdentity(t *testing.T) {
	adapter := New(nil, nil)
	require.Equal(t, ramp.ProviderBridge, adapter.Code())
	require.Equal(t, ramp.KycModeAPIDocs, adapter.PreferredKycMode())
	require.True(t, adapter.Supports(ramp.Capability{Rail: ramp.RailPixPayout}))
}

// Bridge issues its own terms of service and will not create a customer
// without a signed agreement id, so it answers true. The value is what tells
// the service whether to run the ToS step at all.
func TestRequiresTosIsTrueForBridge(t *testing.T) {
	// New takes a ClientFactory and a SecretFactory (kyc_adapter.go:48); both
	// are nil here because RequiresTos is a constant of the provider and makes
	// no call. This file imports `require`, not `assert` — keep to it.
	adapter := New(nil, nil)

	require.True(t, adapter.RequiresTos())
}

// brazilianApplicationBody is what fullApplication put on the wire before the
// tax identity had a type, recorded from that code. A CPF holder living in
// Brazil is every installed build's user, and none of them may see a byte of
// their customer change — including the lower-case issuing countries Bridge
// has always received, which the adapter spells from the contract's capitals —
// but one: base joins the endorsements, the approval the USD rails read.
const brazilianApplicationBody = `{"type":"individual","first_name":"Luiza","last_name":"Souza",` +
	`"email":"luiza@example.com","phone":"+5511999999999","birth_date":"1990-04-02",` +
	`"residential_address":{"street_line_1":"Rua A, 100","city":"Sao Paulo","subdivision":"SP",` +
	`"postal_code":"01000-000","country":"BRA"},"signed_agreement_id":"sa_123","endorsements":["pix","base"],` +
	`"identifying_information":[{"type":"cpf","issuing_country":"bra","number":"39053344705"},` +
	`{"type":"national_id","issuing_country":"bra","image_front":"data:image/jpeg;base64,AAAA",` +
	`"image_back":"data:image/jpeg;base64,BBBB"}],"liveness_check_selfies":[{"image":"data:image/jpeg;base64,CCCC"}]}`

func TestSubmitApplicationKeepsTheBrazilianPayloadByteForByte(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, fullApplication())
	require.NoError(t, err)
	require.Equal(t, brazilianApplicationBody, string(got.raw))
}

// passportApplication is an Argentine passport holder living in Buenos Aires,
// the shape the sandbox accepted on 07/10/2026 (probe S3): the passport is
// the tax identity, typed by the country that issued it, and the address
// names no subdivision.
func passportApplication() ramp.Application {
	app := fullApplication()
	app.Profile.Phone = "+5491100000000"
	app.Profile.Address = ramp.Address{
		StreetLine1: "Avenida Corrientes 1234", City: "Buenos Aires",
		PostalCode: "C1043AAZ", Country: "ARG",
	}
	app.IdentifyingInformation = []ramp.IdentifyingDocument{
		{Type: models.TaxIDTypePassport, IssuingCountry: "ARG", Number: "AB123456"},
	}
	app.LivenessCheckSelfies = nil
	return app
}

func TestSubmitApplicationSendsAPassportAsTheTaxIdentity(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, passportApplication())
	require.NoError(t, err)

	require.Equal(t, []any{map[string]any{"type": "passport", "issuing_country": "arg", "number": "AB123456"}},
		got.body["identifying_information"])
	address, ok := got.body["residential_address"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ARG", address["country"])
	// Absent, not "": the sandbox took AR, GB and PT with no subdivision at
	// all (probe S1), and an empty string is a value it would have to judge.
	require.NotContains(t, address, "subdivision")
}

// Outside Brazil a subdivision reaches the adapter only as a code from the
// list of a country whose addresses must name one: the bridge_subdivision rule
// holds it to that list, and the KYC service's applicationFrom drops the value
// the rule ignores for every other country (subdivisionFor). So it goes as
// given — never read as a Brazilian state — and only a blank one is left out.
func TestSubmitApplicationSendsAForeignSubdivisionAsGiven(t *testing.T) {
	cases := []struct {
		name, country, subdivision string
		want                       any
	}{
		{"a code from the list", "ARE", "DU", "DU"},
		{"a code that is also a Brazilian state", "BWA", "SP", "SP"},
		{"a blank one", "ARG", "   ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
			})
			app := passportApplication()
			app.Profile.Address.Country = c.country
			app.Profile.Address.Subdivision = c.subdivision

			_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
			require.NoError(t, err)

			address := got.body["residential_address"].(map[string]any)
			subdivision, present := address["subdivision"]
			if c.want == nil {
				require.False(t, present, "subdivision must be omitted, got %v", subdivision)
				return
			}
			require.Equal(t, c.want, subdivision)
		})
	}
}

// A passport is one page: Bridge takes its photograph as image_front only,
// so a back the app sent anyway does not travel.
func TestSubmitDocumentsSendsOnlyTheFrontOfAPassport(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{{
			Type: "passport", IssuingCountry: "ARG",
			ImageFront: "data:image/jpeg;base64,AAAA", ImageBack: "data:image/jpeg;base64,BBBB",
		}}})
	require.NoError(t, err)

	require.Equal(t, []any{map[string]any{
		"type": "passport", "issuing_country": "arg", "image_front": "data:image/jpeg;base64,AAAA",
	}}, got.body["identifying_information"])
}

// A national id keeps both sides: the passport rule is about the passport.
func TestSubmitDocumentsKeepsBothSidesOfANationalID(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"under_review"}`))
	})

	_, err := adapter.SubmitDocuments(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"},
		ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{{
			Type: "national_id", IssuingCountry: "BRA",
			ImageFront: "data:image/jpeg;base64,AAAA", ImageBack: "data:image/jpeg;base64,BBBB",
		}}})
	require.NoError(t, err)

	docs := got.body["identifying_information"].([]any)
	require.Equal(t, "data:image/jpeg;base64,BBBB", docs[0].(map[string]any)["image_back"])
}

// usApplication is a resident of San Francisco declaring an SSN by its
// digits, the shape the sandbox accepted on 14/10/2026 (spec §8).
func usApplication() ramp.Application {
	app := fullApplication()
	app.Profile.Phone = "+14155550100"
	app.Profile.Address = ramp.Address{
		StreetLine1: "123 Market St Apt 4", City: "San Francisco",
		Subdivision: "CA", PostalCode: "94103", Country: "USA",
	}
	app.IdentifyingInformation = []ramp.IdentifyingDocument{
		{Type: models.TaxIDTypeSSN, IssuingCountry: "USA", Number: "287654321"},
	}
	app.LivenessCheckSelfies = nil
	return app
}

// An SSN goes as Bridge's examples spell it, issued by usa, with the state;
// and the deprecated umbrella pix is asked as pix_offramp, the half Bridge
// grants a non-Brazilian.
func TestSubmitApplicationSendsAUSResidentsSSN(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
	})

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, usApplication())
	require.NoError(t, err)

	require.Equal(t, []any{map[string]any{"type": "ssn", "issuing_country": "usa", "number": "287-65-4321"}},
		got.body["identifying_information"])
	require.Equal(t, []any{"pix_offramp", "base"}, got.body["endorsements"])
	address := got.body["residential_address"].(map[string]any)
	require.Equal(t, "CA", address["subdivision"])
	require.Equal(t, "USA", address["country"])
}

func TestSubmitApplicationSendsAnITINWithItsDashes(t *testing.T) {
	adapter, got := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"cus_1","status":"incomplete"}`))
	})
	app := usApplication()
	app.IdentifyingInformation = []ramp.IdentifyingDocument{{Type: models.TaxIDTypeITIN, IssuingCountry: "USA", Number: "912781234"}}

	_, err := adapter.SubmitApplication(context.Background(), ramp.CustomerRef{CustomerID: 7}, app)
	require.NoError(t, err)

	require.Equal(t, []any{map[string]any{"type": "itin", "issuing_country": "usa", "number": "912-78-1234"}},
		got.body["identifying_information"])
}

func TestEndorsementsFor(t *testing.T) {
	withEndorsements := func(country string, endorsements ...string) ramp.Application {
		app := usApplication()
		app.Profile.Address.Country = country
		app.Endorsements = endorsements
		return app
	}
	for _, tc := range []struct {
		name string
		app  ramp.Application
		want []string
	}{
		{"brazil keeps the umbrella and asks base", withEndorsements("BRA", "pix"), []string{"pix", "base"}},
		{"a passport abroad keeps what it asked and asks base", withEndorsements("ARG", "pix"), []string{"pix", "base"}},
		{"the united states asks the payout half", withEndorsements("USA", "pix"), []string{"pix_offramp", "base"}},
		{"alpha-2 united states", withEndorsements("us", "pix"), []string{"pix_offramp", "base"}},
		{"no duplicate payout", withEndorsements("USA", "pix", "pix_offramp"), []string{"pix_offramp", "base"}},
		{"the deposit half becomes the payout", withEndorsements("USA", "pix_onramp"), []string{"pix_offramp", "base"}},
		{"no duplicate base", withEndorsements("USA", "base"), []string{"base"}},
		{"where bridge has no usd, no base", withEndorsements("PAK", "pix"), []string{"pix"}},
		{"asking nothing still asks nothing", withEndorsements("BRA"), []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, endorsementsFor(tc.app))
		})
	}
}
