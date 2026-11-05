package blindpay_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// atomicStub is a BlindPay that serves uploads, the customer create and the
// customer read, and records what each received.
type atomicStub struct {
	mu          sync.Mutex
	uploads     []string // filenames, in order
	createBody  []byte
	createKey   string
	createType  string
	creates     int
	createReply string
	// createStatus answers the create; zero is 200.
	createStatus int
	readStatus   int
	requests     int
}

func newAtomicStub(t *testing.T) (*blindpay.Adapter, *atomicStub) {
	t.Helper()
	stub := &atomicStub{createReply: `{"id":"cu_1","customer_id":"cu_1"}`, readStatus: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		stub.requests++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/upload":
			require.NoError(t, r.ParseMultipartForm(32<<20))
			assert.Equal(t, "onboarding", r.FormValue("bucket"))
			_, header, err := r.FormFile("file")
			require.NoError(t, err)
			stub.uploads = append(stub.uploads, header.Filename)
			_, _ = io.WriteString(w, `{"file_url":"https://files.blindpay.test/`+header.Filename+`"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/instances/in_000000000001/customers":
			stub.creates++
			stub.createBody, _ = io.ReadAll(r.Body)
			stub.createKey, stub.createType = r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
			if stub.createStatus != 0 {
				w.WriteHeader(stub.createStatus)
			}
			_, _ = io.WriteString(w, stub.createReply)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/instances/in_000000000001/customers/cu_1":
			w.WriteHeader(stub.readStatus)
			_, _ = io.WriteString(w, customerBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := blindpay.NewClient(blindpay.Config{APIKey: "bp_test_key", InstanceID: "in_000000000001", BaseURL: server.URL + "/v1"})
	return blindpay.New(clientFactory(client, nil), nil, nil), stub
}

func validApplication() ramp.Application {
	return ramp.Application{
		Type:              "individual",
		SignedAgreementID: "to_1",
		Profile: ramp.Profile{
			FirstName: "Ana", LastName: "Souza", Email: "ana@example.com",
			Phone: "+5511987654321", BirthDate: "1990-04-02",
			Address: ramp.Address{
				StreetLine1: "Av. Paulista", Number: "1000", StreetLine2: "Apto 12", Neighborhood: "Bela Vista",
				City: "São Paulo", Subdivision: "BR-SP", PostalCode: "01310-100", Country: "BRA",
			},
			Occupation: "Engineer", SourceOfFunds: "employment_salary", AccountPurpose: "pay_goods_services",
		},
		IdentifyingInformation: []ramp.IdentifyingDocument{{Type: "cpf", IssuingCountry: "BRA", Number: "12345678909"}},
	}
}

func validDocuments(withBack bool) ramp.Documents {
	doc := ramp.IdentifyingDocument{Type: "national_id", IssuingCountry: "BRA", ImageFront: dataURI("image/jpeg", jpegBytes)}
	if withBack {
		doc.ImageBack = dataURI("image/png", pngBytes)
	}
	return ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{doc},
		LivenessCheckSelfies:   []ramp.Selfie{{Image: dataURI("image/png", pngBytes)}},
		ClientIPAddress:        "::ffff:203.0.113.7",
	}
}

func submission(withBack bool) ramp.AtomicKycSubmission {
	return ramp.AtomicKycSubmission{
		SubmissionID: "sub_1", Reference: "xip-kyc-42",
		Application: validApplication(), Documents: validDocuments(withBack),
	}
}

func digest(submissionID, field, mime string, data []byte) string {
	sum := sha256.Sum256(append([]byte(submissionID+"\x00"+field+"\x00"+mime+"\x00"), data...))
	return hex.EncodeToString(sum[:])
}

// persisted records every checkpoint persist was called with.
type persisted struct{ calls []ramp.AtomicKycCheckpoint }

func (p *persisted) persist(cp ramp.AtomicKycCheckpoint) error {
	p.calls = append(p.calls, cp)
	return nil
}

func TestValidateDeferredApplication_AnswersTheWaitingState(t *testing.T) {
	adapter := blindpay.New(nil, nil, nil)
	state, err := adapter.ValidateDeferredApplication(validApplication())
	require.NoError(t, err)
	assert.Equal(t, &ramp.KycState{
		Status: models.KycStatusIncomplete, TosStatus: models.TosStatusApproved, SignedAgreementID: "to_1",
		Requirements: []string{"government_id", "selfie"},
		Endorsements: []ramp.Endorsement{
			{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusIncomplete},
			{Name: models.EndorsementPix, Status: models.EndorsementStatusIncomplete},
		},
	}, state)
}

func TestValidateDeferredApplication_TakesTheRetainedTaxID(t *testing.T) {
	app := validApplication()
	app.IdentifyingInformation = nil
	app.Profile.TaxID = "12345678909"
	_, err := blindpay.New(nil, nil, nil).ValidateDeferredApplication(app)
	require.NoError(t, err)
}

func TestValidateDeferredApplication_Refusals(t *testing.T) {
	cases := map[string]func(*ramp.Application){
		"no first name":       func(a *ramp.Application) { a.Profile.FirstName = "" },
		"no last name":        func(a *ramp.Application) { a.Profile.LastName = " " },
		"no email":            func(a *ramp.Application) { a.Profile.Email = "" },
		"no birth date":       func(a *ramp.Application) { a.Profile.BirthDate = "" },
		"birth date format":   func(a *ramp.Application) { a.Profile.BirthDate = "02/04/1990" },
		"birth date not real": func(a *ramp.Application) { a.Profile.BirthDate = "1990-02-30" },
		"phone not E.164":     func(a *ramp.Application) { a.Profile.Phone = "11987654321" },
		"no street":           func(a *ramp.Application) { a.Profile.Address.StreetLine1 = "" },
		"no city":             func(a *ramp.Application) { a.Profile.Address.City = "" },
		"no postal code":      func(a *ramp.Application) { a.Profile.Address.PostalCode = "" },
		"unknown country":     func(a *ramp.Application) { a.Profile.Address.Country = "XXX" },
		"no tax id":           func(a *ramp.Application) { a.IdentifyingInformation = nil },
		"no agreement":        func(a *ramp.Application) { a.SignedAgreementID = "" },
		"business":            func(a *ramp.Application) { a.Type = "business" },
		"US state not 2 chars": func(a *ramp.Application) {
			a.Profile.Address.Country = "USA"
			a.Profile.Address.Subdivision = "California"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			app := validApplication()
			mutate(&app)
			_, err := blindpay.New(nil, nil, nil).ValidateDeferredApplication(app)
			require.ErrorIs(t, err, ramp.ErrKycDataInvalid)
		})
	}
}

func TestValidateAtomicDocuments_AcceptsFrontAloneOrFrontAndBack(t *testing.T) {
	adapter := blindpay.New(nil, nil, nil)
	require.NoError(t, adapter.ValidateAtomicDocuments(validDocuments(false)))
	require.NoError(t, adapter.ValidateAtomicDocuments(validDocuments(true)))
}

func TestValidateAtomicDocuments_RefusesBeforeAnyRequest(t *testing.T) {
	cases := map[string]func(*ramp.Documents){
		"no selfie": func(d *ramp.Documents) { d.LivenessCheckSelfies = nil },
		"two selfies": func(d *ramp.Documents) {
			d.LivenessCheckSelfies = append(d.LivenessCheckSelfies, d.LivenessCheckSelfies[0])
		},
		"gif selfie":           func(d *ramp.Documents) { d.LivenessCheckSelfies[0].Image = dataURI("image/gif", gifBytes) },
		"declared png is jpeg": func(d *ramp.Documents) { d.IdentifyingInformation[0].ImageFront = dataURI("image/png", jpegBytes) },
		"bad back":             func(d *ramp.Documents) { d.IdentifyingInformation[0].ImageBack = dataURI("image/gif", gifBytes) },
		"no document":          func(d *ramp.Documents) { d.IdentifyingInformation = nil },
		"two documents": func(d *ramp.Documents) {
			d.IdentifyingInformation = append(d.IdentifyingInformation, d.IdentifyingInformation[0])
		},
		"back without front": func(d *ramp.Documents) {
			d.IdentifyingInformation[0].ImageBack, d.IdentifyingInformation[0].ImageFront = d.IdentifyingInformation[0].ImageFront, ""
		},
		"unknown type":    func(d *ramp.Documents) { d.IdentifyingInformation[0].Type = "residence_permit" },
		"unknown country": func(d *ramp.Documents) { d.IdentifyingInformation[0].IssuingCountry = "ZZZ" },
		"unspecified IP":  func(d *ramp.Documents) { d.ClientIPAddress = "0.0.0.0" },
		"no IP":           func(d *ramp.Documents) { d.ClientIPAddress = "" },
		"not an IP":       func(d *ramp.Documents) { d.ClientIPAddress = "localhost" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, stub := newAtomicStub(t)
			docs := validDocuments(true)
			mutate(&docs)
			require.ErrorIs(t, adapter.ValidateAtomicDocuments(docs), ramp.ErrKycDataInvalid)

			sub := submission(true)
			sub.Documents = docs
			var p persisted
			_, err := adapter.PrepareCustomer(context.Background(), sub, ramp.AtomicKycCheckpoint{}, p.persist)
			require.ErrorIs(t, err, ramp.ErrKycDataInvalid)
			assert.Zero(t, stub.requests, "nothing was sent")
			assert.Empty(t, p.calls)
		})
	}
}

func TestPrepareCustomer_RevalidatesTheApplication(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	sub := submission(false)
	sub.Application.SignedAgreementID = ""
	var p persisted
	_, err := adapter.PrepareCustomer(context.Background(), sub, ramp.AtomicKycCheckpoint{}, p.persist)
	require.ErrorIs(t, err, ramp.ErrKycDataInvalid)
	assert.Zero(t, stub.requests)
}

func TestPrepareCustomer_UploadsEachFileAndPersistsAfterEach(t *testing.T) {
	for _, withBack := range []bool{false, true} {
		t.Run(map[bool]string{false: "front only", true: "front and back"}[withBack], func(t *testing.T) {
			adapter, stub := newAtomicStub(t)
			var p persisted
			prepared, err := adapter.PrepareCustomer(context.Background(), submission(withBack), ramp.AtomicKycCheckpoint{}, p.persist)
			require.NoError(t, err)

			want := []string{"id_doc_front_file.jpg", "selfie_file.png"}
			if withBack {
				want = []string{"id_doc_front_file.jpg", "id_doc_back_file.png", "selfie_file.png"}
			}
			assert.Equal(t, want, stub.uploads)
			require.Len(t, p.calls, len(want), "persist after every upload")
			for i, cp := range p.calls {
				assert.Equal(t, 1, cp.Version)
				assert.Equal(t, "sub_1", cp.SubmissionID)
				assert.Len(t, cp.Fields, i+1, "each persist carries the upload just made")
			}
			last := p.calls[len(p.calls)-1].Fields
			assert.Equal(t, "id_doc_front_file", last[0].Field)
			assert.Equal(t, digest("sub_1", "id_doc_front_file", "image/jpeg", jpegBytes), last[0].DocumentSHA256)
			assert.Equal(t, "https://files.blindpay.test/id_doc_front_file.jpg", last[0].ProviderHandle)

			assert.Equal(t, 1, prepared.Version)
			sum := sha256.Sum256(prepared.Payload)
			assert.Equal(t, hex.EncodeToString(sum[:]), prepared.PayloadSHA256)
			assert.Zero(t, stub.creates, "preparing never creates")
		})
	}
}

func TestPrepareCustomer_ReusesAFieldAlreadyUploaded(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	checkpoint := ramp.AtomicKycCheckpoint{Version: 1, SubmissionID: "sub_1", Fields: []ramp.AtomicKycUploadedField{{
		Field: "id_doc_front_file", DocumentSHA256: digest("sub_1", "id_doc_front_file", "image/jpeg", jpegBytes),
		ProviderHandle: "https://files.blindpay.test/earlier-front.jpg",
	}}}
	var p persisted
	prepared, err := adapter.PrepareCustomer(context.Background(), submission(true), checkpoint, p.persist)
	require.NoError(t, err)
	assert.Equal(t, []string{"id_doc_back_file.png", "selfie_file.png"}, stub.uploads)
	assert.Len(t, p.calls, 2)
	assert.Contains(t, string(prepared.Payload), `"id_doc_front_file":"https://files.blindpay.test/earlier-front.jpg"`)
}

func TestPrepareCustomer_UploadsAgainWhenTheFileChanged(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	checkpoint := ramp.AtomicKycCheckpoint{Version: 1, SubmissionID: "sub_1", Fields: []ramp.AtomicKycUploadedField{{
		Field: "id_doc_front_file", DocumentSHA256: digest("sub_1", "id_doc_front_file", "image/jpeg", []byte("another file")),
		ProviderHandle: "https://files.blindpay.test/stale.jpg",
	}}}
	var p persisted
	prepared, err := adapter.PrepareCustomer(context.Background(), submission(false), checkpoint, p.persist)
	require.NoError(t, err)
	assert.Equal(t, []string{"id_doc_front_file.jpg", "selfie_file.png"}, stub.uploads)
	assert.NotContains(t, string(prepared.Payload), "stale.jpg")
	final := p.calls[len(p.calls)-1].Fields
	require.Len(t, final, 2, "the stale entry is replaced, not duplicated")
	assert.Equal(t, "https://files.blindpay.test/id_doc_front_file.jpg", final[0].ProviderHandle)
	assert.Equal(t, "https://files.blindpay.test/stale.jpg", checkpoint.Fields[0].ProviderHandle, "the caller's checkpoint is not written to")
}

func TestPrepareCustomer_AFailedPersistStopsBeforeTheNextUpload(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	boom := errors.New("db down")
	_, err := adapter.PrepareCustomer(context.Background(), submission(true), ramp.AtomicKycCheckpoint{},
		func(ramp.AtomicKycCheckpoint) error { return boom })
	require.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"id_doc_front_file.jpg"}, stub.uploads)
}

func TestPrepareCustomer_RefusesAnotherSubmissionsCheckpoint(t *testing.T) {
	cases := map[string]ramp.AtomicKycCheckpoint{
		"other submission": {Version: 1, SubmissionID: "sub_2"},
		"other version":    {Version: 2, SubmissionID: "sub_1"},
		"no version":       {SubmissionID: "sub_1"},
	}
	for name, cp := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, stub := newAtomicStub(t)
			var p persisted
			_, err := adapter.PrepareCustomer(context.Background(), submission(false), cp, p.persist)
			require.ErrorIs(t, err, ramp.ErrAtomicMaterialInvalid)
			assert.NotErrorIs(t, err, ramp.ErrRequestNotSent, "a mismatch is the same on every retry")
			assert.Zero(t, stub.requests)
		})
	}
}

func TestPrepareCustomer_IsDeterministicFromACompleteCheckpoint(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	var p persisted
	first, err := adapter.PrepareCustomer(context.Background(), submission(true), ramp.AtomicKycCheckpoint{}, p.persist)
	require.NoError(t, err)
	complete := p.calls[len(p.calls)-1]

	second, err := adapter.PrepareCustomer(context.Background(), submission(true), complete, p.persist)
	require.NoError(t, err)
	third, err := adapter.PrepareCustomer(context.Background(), submission(true), complete, p.persist)
	require.NoError(t, err)
	assert.Equal(t, string(first.Payload), string(second.Payload))
	assert.Equal(t, string(second.Payload), string(third.Payload))
	assert.Equal(t, first.PayloadSHA256, third.PayloadSHA256)
	assert.Len(t, stub.uploads, 3, "a complete checkpoint uploads nothing")
}

func prepared(t *testing.T, adapter *blindpay.Adapter) ramp.PreparedAtomicKyc {
	t.Helper()
	var p persisted
	out, err := adapter.PrepareCustomer(context.Background(), submission(false), ramp.AtomicKycCheckpoint{}, p.persist)
	require.NoError(t, err)
	return *out
}

func TestCreatePreparedCustomer_SendsThePreparedBytes(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	body := prepared(t, adapter)
	state, err := adapter.CreatePreparedCustomer(context.Background(), "kyc-docs:sub_1", body)
	require.NoError(t, err)
	assert.Equal(t, []byte(body.Payload), stub.createBody, "byte for byte")
	assert.Equal(t, "kyc-docs:sub_1", stub.createKey)
	assert.Equal(t, "application/json", stub.createType)
	assert.Equal(t, "cu_1", state.ExternalCustomerID)
	assert.Equal(t, models.KycStatusRequiresInfo, state.Status)
}

func TestCreatePreparedCustomer_AFailedReadStillAnswersTheID(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	body := prepared(t, adapter)
	stub.readStatus = http.StatusInternalServerError
	state, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
	require.Error(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "cu_1", state.ExternalCustomerID)
}

func TestCreatePreparedCustomer_RefusesAnUnusableAnswer(t *testing.T) {
	for name, reply := range map[string]string{
		"ids differ": `{"id":"cu_1","customer_id":"cu_2"}`,
		"no id":      `{"id":"","customer_id":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			adapter, stub := newAtomicStub(t)
			body := prepared(t, adapter)
			stub.createReply = reply
			state, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.Nil(t, state)
		})
	}
}

func TestCreatePreparedCustomer_RefusesATamperedBodyBeforeSending(t *testing.T) {
	adapter, stub := newAtomicStub(t)
	body := prepared(t, adapter)
	cases := map[string]ramp.PreparedAtomicKyc{
		"other version": {Version: 2, Payload: body.Payload, PayloadSHA256: body.PayloadSHA256},
		"digest differs": {Version: 1, Payload: []byte(strings.Replace(string(body.Payload), "Ana", "Bia", 1)),
			PayloadSHA256: body.PayloadSHA256},
		"empty": {Version: 1},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := adapter.CreatePreparedCustomer(context.Background(), "k", p)
			require.ErrorIs(t, err, ramp.ErrAtomicMaterialInvalid)
			assert.NotErrorIs(t, err, ramp.ErrRequestNotSent, "a mismatch is the same on every retry")
		})
	}
	_, err := adapter.CreatePreparedCustomer(context.Background(), "", body)
	require.ErrorIs(t, err, ramp.ErrAtomicMaterialInvalid, "no idempotency key")
	assert.Zero(t, stub.creates)
}

// A 400 or 422 on the create is BlindPay refusing the person: no customer
// was made, and the service may end the attempt. Anything else — the auth
// refusal, a key replayed with another body, a 5xx — proves nothing.
func TestCreatePreparedCustomer_NamesARefusedCreate(t *testing.T) {
	for status, refused := range map[int]bool{
		http.StatusBadRequest: true, http.StatusUnprocessableEntity: true,
		http.StatusUnauthorized: false, http.StatusConflict: false, http.StatusInternalServerError: false,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			adapter, stub := newAtomicStub(t)
			body := prepared(t, adapter)
			stub.createStatus = status
			stub.createReply = `{"message":"invalid_field","code":""}`
			_, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.Equal(t, refused, errors.Is(err, ramp.ErrKycDataInvalid))
		})
	}

	adapter, stub := newAtomicStub(t)
	body := prepared(t, adapter)
	stub.createStatus = http.StatusBadRequest
	stub.createReply = `{"message":"idempotency_key_payload_mismatch"}`
	_, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
	require.ErrorIs(t, err, ramp.ErrIdempotencyConflict)
	assert.NotErrorIs(t, err, ramp.ErrKycDataInvalid, "a replayed key proves nothing")
}

// BlindPay answering that the terms were already accepted, or the customer
// already approved, says a customer may exist: it is never the refusal that
// would let the service end the attempt.
func TestCreatePreparedCustomer_AnAlreadyExistingCustomerIsNoRefusal(t *testing.T) {
	for _, code := range []string{"TOS_ALREADY_ACCEPTED", "CUSTOMERS_ALREADY_APPROVED"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
			t.Run(fmt.Sprintf("%s %d", code, status), func(t *testing.T) {
				adapter, stub := newAtomicStub(t)
				body := prepared(t, adapter)
				stub.createStatus = status
				stub.createReply = `{"code":"` + code + `","message":"already"}`
				state, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
				require.ErrorIs(t, err, ramp.ErrProviderFailed)
				assert.NotErrorIs(t, err, ramp.ErrKycDataInvalid)
				assert.Nil(t, state)
			})
		}
	}
}

// A create that reached BlindPay and got no answer back is unknown upstream
// state, which the service replays under the same key; a 2xx that came back
// unreadable is an answer, and stays ErrProviderFailed alone.
func TestCreatePreparedCustomer_TellsNoAnswerFromAnUnusableOne(t *testing.T) {
	t.Run("timed out", func(t *testing.T) {
		adapter, _ := newAtomicStub(t)
		body := prepared(t, adapter)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(func() { close(release); server.Close() })
		client := blindpay.NewClient(blindpay.Config{APIKey: "bp_test_key", InstanceID: "in_000000000001",
			BaseURL: server.URL + "/v1", Timeout: 50 * time.Millisecond})
		_, err := blindpay.New(clientFactory(client, nil), nil, nil).CreatePreparedCustomer(context.Background(), "k", body)
		require.ErrorIs(t, err, ramp.ErrOutcomeUnknown)
		require.ErrorIs(t, err, ramp.ErrProviderFailed)
		assert.NotErrorIs(t, err, ramp.ErrRequestNotSent)
		assert.NotErrorIs(t, err, ramp.ErrKycDataInvalid)
	})
	t.Run("2xx that does not decode", func(t *testing.T) {
		adapter, stub := newAtomicStub(t)
		body := prepared(t, adapter)
		stub.createReply = `{"id":`
		_, err := adapter.CreatePreparedCustomer(context.Background(), "k", body)
		require.ErrorIs(t, err, ramp.ErrProviderFailed)
		assert.NotErrorIs(t, err, ramp.ErrOutcomeUnknown, "an answer came back")
	})
}

func TestABrazilianApplicationBuildsABRCustomer(t *testing.T) {
	sub := submission(true)
	sub.Application.IdentifyingInformation = []ramp.IdentifyingDocument{{Type: models.TaxIDTypeCPF, IssuingCountry: "BRA", Number: "390.533.447-05"}}
	body := preparedBody(t, sub)
	assert.Equal(t, "BR", body["country"])
	assert.Equal(t, "SP", body["state_province_region"])
	assert.Equal(t, "39053344705", body["tax_id"])

	state, err := blindpay.New(nil, nil, nil).ValidateDeferredApplication(sub.Application)
	require.NoError(t, err)
	assert.Equal(t, models.KycStatusIncomplete, state.Status)
	assert.Equal(t, []string{"government_id", "selfie"}, state.Requirements)
}
