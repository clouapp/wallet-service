package ozav

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// pngBytes is a real 1x1 PNG. The bytes matter: the adapter hashes what it
// decodes and Ozav answers with a digest of what it received, so a
// placeholder string would not exercise the one check that proves the upload
// was not truncated.
var pngBytes = func() []byte {
	raw, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return raw
}()

func pngDataURI() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// documentsServer is a fake Ozav that answers the two calls an upload takes:
// the declare, and the PUT of the bytes. It records every request so a test
// can assert what did NOT happen, which is what the allow-list and the
// mime guard are about.
type documentsServer struct {
	mu       sync.Mutex
	requests []recordedRequest
	// base is the server's own https address, filled in by the wiring.
	base string
	// uploadURL overrides the address handed back in upload_url. Empty means
	// "point at this server", which is the normal path.
	uploadURL string
	// digest overrides the sha256 the fake reports for received bytes. Empty
	// means "report the digest we actually computed".
	digest string
}

func (s *documentsServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, recordedRequest{r.Method, r.URL.Path, r.Header.Clone(), body})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/documents"):
		upload := s.uploadURL
		if upload == "" {
			upload = s.base + "/uploads/doc_1"
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ozav_doc_1", "object": "document", "status": "pending_upload",
			"upload_url": upload, "upload_expires_at": "2026-08-28T21:00:00Z",
		})
	case r.Method == http.MethodPut:
		digest := s.digest
		if digest == "" {
			digest = sha256Hex(body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ozav_doc_1", "object": "document", "status": "received", "sha256": digest,
		})
	default: // the GET /customers/{id} that reports the state afterwards
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ozav_cus_1", "status": "pending",
			"requirements": map[string]any{"due": []string{}, "missing_fields": []string{}},
		})
	}
}

func (s *documentsServer) calls(method string) []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []recordedRequest{}
	for _, req := range s.requests {
		if req.method == method {
			out = append(out, req)
		}
	}
	return out
}

// newDocumentsAdapter wires an adapter to the fake over TLS, because the
// upload carries the API key and the client refuses to send it in the clear
// — a plain httptest server would be testing a path production never takes.
// The allow-list is the fake's own host; production's real list lives in
// NewClient, and a test that reached for it would be asserting on the
// internet.
func newDocumentsAdapter(t *testing.T, fake *documentsServer) *Adapter {
	return newDocumentsAdapterAllowing(t, fake, "127.0.0.1")
}

// newDocumentsAdapterAllowing is the same wiring with the allow-list named
// explicitly, so one test can hand the adapter an upload URL its list does
// not cover while still pointing at a server that would record the request
// if the guard were missing.
func newDocumentsAdapterAllowing(t *testing.T, fake *documentsServer, hosts ...string) *Adapter {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	t.Cleanup(server.Close)
	fake.base = server.URL

	client := NewClient(Config{
		APIKey: "ozav_sk_test_abc123", Timeout: 2 * time.Second,
		BaseURL: server.URL, HTTPClient: server.Client(), UploadHosts: hosts,
	})
	return &Adapter{clients: func(context.Context) (*Client, error) { return client, nil }}
}

func docsRef() ramp.CustomerRef {
	return ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}
}

// The two-step upload, end to end: what XIP holds is a data-URI, what Ozav
// wants is a declaration of the bytes followed by the bytes themselves.
func TestSubmitDocumentsDeclaresThenPutsTheBytes(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "national_id", IssuingCountry: "bra", ImageFront: pngDataURI()},
		},
	})
	require.NoError(t, err)

	declares := fake.calls(http.MethodPost)
	require.Len(t, declares, 1)
	var declared map[string]any
	require.NoError(t, json.Unmarshal(declares[0].body, &declared))
	require.Equal(t, "identity_document", declared["doc_type"])
	require.Equal(t, "front", declared["side"])
	require.Equal(t, "BR", declared["issuing_country"], "Ozav wants alpha-2, XIP carries alpha-3")
	require.Equal(t, "image/png", declared["mime"])
	require.Equal(t, float64(len(pngBytes)), declared["size_bytes"])
	require.Equal(t, sha256Hex(pngBytes), declared["sha256"])

	uploads := fake.calls(http.MethodPut)
	require.Len(t, uploads, 1)
	require.Equal(t, pngBytes, uploads[0].body, "the PUT carries raw bytes, not the data-URI")
	require.Equal(t, "image/png", uploads[0].header.Get("Content-Type"))
}

// The upload_token in the URL scopes the grant; it does not authenticate.
// Ozav rejects a PUT without the key, and the failure would be a document
// stuck in pending_upload forever.
func TestSubmitDocumentsSendsTheAPIKeyOnTheUpload(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
	})
	require.NoError(t, err)

	uploads := fake.calls(http.MethodPut)
	require.Len(t, uploads, 1)
	require.Equal(t, "ozav_sk_test_abc123", uploads[0].header.Get("X-API-Key"))
}

// Both faces of a document and the selfie each become their own upload, with
// the doc_type and side Ozav's enum names. `liveness` is deliberately not
// used: on Ozav it designates a proof of presence, and what XIP holds is a
// still image.
func TestSubmitDocumentsMapsEveryImageToItsDocType(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "national_id", IssuingCountry: "bra", ImageFront: pngDataURI(), ImageBack: pngDataURI()},
		},
		LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
		ProofOfAddress:       pngDataURI(),
	})
	require.NoError(t, err)

	got := [][2]string{}
	for _, req := range fake.calls(http.MethodPost) {
		var declared map[string]any
		require.NoError(t, json.Unmarshal(req.body, &declared))
		got = append(got, [2]string{declared["doc_type"].(string), declared["side"].(string)})
	}
	require.Equal(t, [][2]string{
		{"identity_document", "front"},
		{"identity_document", "back"},
		{"selfie", "single"},
		{"proof_of_address", "single"},
	}, got)
}

// A proof of address is declared under Ozav's own category, single-sided and
// with no issuing country: it is a utility bill, not a state document.
func TestSubmitDocumentsDeclaresAProofOfAddress(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		ProofOfAddress: pngDataURI(),
	})
	require.NoError(t, err)

	declares := fake.calls(http.MethodPost)
	require.Len(t, declares, 1)
	var declared map[string]any
	require.NoError(t, json.Unmarshal(declares[0].body, &declared))
	require.Equal(t, "proof_of_address", declared["doc_type"])
	require.Equal(t, "single", declared["side"])
	require.NotContains(t, declared, "issuing_country")
}

// A statement runs to several pages, so it goes up as the PDF the bank issued,
// not as a picture of its first page: the declare names the pdf mime and the
// bytes are the file's own.
func TestSubmitDocumentsUploadsABankStatementAsThePDFItIs(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		BankStatement: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdf),
	})
	require.NoError(t, err)

	declares := fake.calls(http.MethodPost)
	require.Len(t, declares, 1)
	var declared map[string]any
	require.NoError(t, json.Unmarshal(declares[0].body, &declared))
	require.Equal(t, "bank_statement", declared["doc_type"])
	require.Equal(t, "single", declared["side"])
	require.Equal(t, "application/pdf", declared["mime"])
	require.Equal(t, sha256Hex(pdf), declared["sha256"])

	puts := fake.calls(http.MethodPut)
	require.Len(t, puts, 1)
	require.Equal(t, pdf, puts[0].body)
}

// A CPF is not a document. It reaches Ozav as individual.tax_id on the
// customer, and declaring it here would create a document with no bytes to
// follow.
func TestSubmitDocumentsIgnoresAnEntryWithNoImages(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "cpf", Number: "39053344705"},
			{Type: "national_id", IssuingCountry: "bra", ImageFront: pngDataURI()},
		},
	})
	require.NoError(t, err)

	require.Len(t, fake.calls(http.MethodPost), 1)
}

// A mime Ozav does not accept is refused before anything is declared.
// Declaring first would leave a document in pending_upload that no later
// call can ever complete, and Ozav has no delete.
func TestSubmitDocumentsRefusesAnUnsupportedMimeBeforeDeclaring(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "national_id", ImageFront: "data:image/gif;base64," + base64.StdEncoding.EncodeToString(pngBytes)},
		},
	})

	require.ErrorIs(t, err, ramp.ErrDocumentsNotSupported)
	require.Empty(t, fake.requests, "nothing may be declared once one image is unusable")
}

// Same rule for a payload that is not a data-URI at all: the whole batch is
// validated before the first call, so a bad entry cannot leave earlier ones
// half-uploaded.
func TestSubmitDocumentsRefusesAMalformedDataURIBeforeDeclaring(t *testing.T) {
	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "national_id", ImageFront: pngDataURI()},
			{Type: "passport", ImageFront: "not-a-data-uri"},
		},
	})

	require.Error(t, err)
	require.Empty(t, fake.requests)
}

// The upload URL arrives in a response body and the PUT has to carry the
// secret key, so an unrecognised host is a way to make XIP hand its
// credential to whoever shaped that response. The refusal has to come before
// the request is dialed, and the proof is that the other server saw nothing.
func TestSubmitDocumentsRefusesAnUploadHostOutsideTheAllowList(t *testing.T) {
	fake := &documentsServer{}
	// The allow-list is production's, and the upload_url points at the test
	// server, which is not on it. The server is the proof: were the guard
	// missing, the PUT would arrive and be recorded.
	adapter := newDocumentsAdapterAllowing(t, fake, "storage.ozav.io")

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
	})

	require.ErrorIs(t, err, ramp.ErrUploadHostNotAllowed)
	require.NotErrorIs(t, err, ramp.ErrProviderFailed, "the refusal is ours; Ozav was never asked")
	require.Empty(t, fake.calls(http.MethodPut), "the key must not reach a host we do not trust")
}

// And the same refusal for a cleartext URL on a host that IS on the list:
// the key would go out readable on the wire.
func TestSubmitDocumentsRefusesACleartextUploadURL(t *testing.T) {
	fake := &documentsServer{uploadURL: "http://storage.ozav.io/uploads/doc_1"}
	adapter := newDocumentsAdapterAllowing(t, fake, "storage.ozav.io", "127.0.0.1")

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
	})

	require.ErrorIs(t, err, ramp.ErrUploadHostNotAllowed)
}

// Ozav computes the digest over the bytes it received. It disagreeing with
// ours is the only signal that the upload was truncated — the PUT still
// answers 200.
func TestSubmitDocumentsFailsWhenOzavsDigestDisagrees(t *testing.T) {
	fake := &documentsServer{digest: sha256Hex([]byte("other bytes"))}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "sha256")
}

// Two documents can legitimately share a doc_type and a side — a national id
// and a passport are both identity_document/front. If they also shared an
// idempotency key, Ozav would answer the second declare with the first
// document's grant, and the second document's bytes would be uploaded against
// the first. The sha256 check cannot catch it: the digest Ozav reports is of
// the bytes it just received, so it agrees. The corruption would be silent.
func TestSubmitDocumentsGivesEachDocumentItsOwnIdempotencyKey(t *testing.T) {
	other := append([]byte{}, pngBytes...)
	other = append(other, 0x00)

	fake := &documentsServer{}
	adapter := newDocumentsAdapter(t, fake)

	_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{
			{Type: "national_id", IssuingCountry: "bra", ImageFront: pngDataURI()},
			{Type: "passport", IssuingCountry: "bra",
				ImageFront: "data:image/png;base64," + base64.StdEncoding.EncodeToString(other)},
		},
		IdempotencyKey: "kyc-docs-stg_1",
	})
	require.NoError(t, err)

	declares := fake.calls(http.MethodPost)
	require.Len(t, declares, 2)
	require.NotEqual(t,
		declares[0].header.Get("Idempotency-Key"),
		declares[1].header.Get("Idempotency-Key"),
		"two identity_document/front declares must not be one replayed request")
}

// And the key still has to be stable: a retry of the same submission must
// collide with itself, which is the entire reason an idempotency key exists.
func TestSubmitDocumentsRepeatsTheSameKeyOnARetry(t *testing.T) {
	keys := [][]string{}
	for range 2 {
		fake := &documentsServer{}
		adapter := newDocumentsAdapter(t, fake)

		_, err := adapter.SubmitDocuments(context.Background(), docsRef(), ramp.Documents{
			LivenessCheckSelfies: []ramp.Selfie{{Image: pngDataURI()}},
			IdempotencyKey:       "kyc-docs-stg_1",
		})
		require.NoError(t, err)

		round := []string{}
		for _, declare := range fake.calls(http.MethodPost) {
			round = append(round, declare.header.Get("Idempotency-Key"))
		}
		keys = append(keys, round)
	}
	require.Equal(t, keys[0], keys[1])
}

func TestUploadsFromMapsTheIssuingCountryToAlpha2(t *testing.T) {
	uploads, err := uploadsFrom(ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{
		{Type: "passport", IssuingCountry: "PRT", ImageFront: pngDataURI()},
	}})
	require.NoError(t, err)
	require.Len(t, uploads, 1)
	require.Equal(t, "PT", uploads[0].issuingCountry)

	_, err = uploadsFrom(ramp.Documents{IdentifyingInformation: []ramp.IdentifyingDocument{
		{Type: "passport", IssuingCountry: "ZZZ", ImageFront: pngDataURI()},
	}})
	require.EqualError(t, err, `ozav: unrecognized country code "ZZZ"`)
}
