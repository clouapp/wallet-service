package support

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/http/resources"
)

func TestHTTP_Suite_AssertError(t *testing.T) {
	RunSuite(t, new(assertErrorSuite))
}

type assertErrorSuite struct {
	HTTPSuite
}

func (s *assertErrorSuite) TestReads_The_EnvelopeThisBranchReturns() {
	s.AssertError(recorder(http.StatusNotFound, encode(s.T(), resources.NewError(resources.ErrorDeps{
		Code:    resources.CodeNotFound,
		Message: "wallet not found",
	}))), http.StatusNotFound, resources.CodeNotFound, "wallet not found")

	s.AssertError(recorder(http.StatusUnprocessableEntity, encode(s.T(), resources.NewError(resources.ErrorDeps{
		Code:    resources.CodeWalletNotGasReady,
		Message: resources.CodeWalletNotGasReady,
	}).With(map[string]any{"action": "fund_base_address"}))), http.StatusUnprocessableEntity, resources.CodeWalletNotGasReady, resources.CodeWalletNotGasReady)

	s.AssertError(recorder(http.StatusUnprocessableEntity, encode(s.T(), resources.NewValidation(map[string][]string{
		"email": {"Email address is required"},
	}))), http.StatusUnprocessableEntity, resources.CodeValidationFailed, resources.ValidationMessage)
}

func TestMatch_Error_Envelope(t *testing.T) {
	ok := encode(t, resources.NewError(resources.ErrorDeps{
		Code:    resources.CodeNotFound,
		Message: "wallet not found",
	}))
	cases := []struct {
		name    string
		rec     *httptest.ResponseRecorder
		status  int
		code    string
		message string
		wantErr bool
	}{
		{
			name:    "envelope",
			rec:     recorder(http.StatusNotFound, ok),
			status:  http.StatusNotFound,
			code:    resources.CodeNotFound,
			message: "wallet not found",
		},
		{
			name:    "string error",
			rec:     recorder(http.StatusNotFound, []byte(`{"error":"wallet not found"}`)),
			status:  http.StatusNotFound,
			code:    resources.CodeNotFound,
			message: "wallet not found",
			wantErr: true,
		},
		{
			name:    "wrong status",
			rec:     recorder(http.StatusConflict, ok),
			status:  http.StatusNotFound,
			code:    resources.CodeNotFound,
			message: "wallet not found",
			wantErr: true,
		},
		{
			name:    "wrong code",
			rec:     recorder(http.StatusNotFound, ok),
			status:  http.StatusNotFound,
			code:    resources.CodeConflict,
			message: "wallet not found",
			wantErr: true,
		},
		{
			name:    "wrong message",
			rec:     recorder(http.StatusNotFound, ok),
			status:  http.StatusNotFound,
			code:    resources.CodeNotFound,
			message: "other",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := matchErrorEnvelope(tc.rec, tc.status, tc.code, tc.message)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a mismatch")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExternal_SignsTheBodyWhenTheTokenRequiresIt(t *testing.T) {
	body := `{"external_user_id":"user_signed"}`
	token := Token{Bearer: "raw-jwt", Sign: Signer("raw-jwt")}
	signed := credentialHeaders(tokenCredential(token), body, true, true)
	if signed["Authorization"] != "Bearer raw-jwt" {
		t.Fatalf("authorization = %q", signed["Authorization"])
	}
	if signed["X-Signature"] != token.Sign([]byte(body)) {
		t.Fatalf("signature = %q", signed["X-Signature"])
	}

	unsigned := credentialHeaders(tokenCredential(Token{Bearer: "raw-jwt"}), body, true, true)
	if _, ok := unsigned["X-Signature"]; ok {
		t.Fatal("token without a signer attached X-Signature")
	}

	// A GET has no body, so the signer is not applied.
	get := credentialHeaders(tokenCredential(token), "", true, false)
	if _, ok := get["X-Signature"]; ok {
		t.Fatal("GET attached X-Signature")
	}
}

func TestRequest_Body_KeepsTheBytesASignatureCovers(t *testing.T) {
	payload, reader, err := requestBody(`{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if payload != `{"a":1}` {
		t.Fatalf("payload = %q", payload)
	}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != payload {
		t.Fatalf("reader = %q err=%v", got, err)
	}

	payload, reader, err = requestBody(strings.NewReader(`{"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if payload != `{"b":2}` || reader == nil {
		t.Fatalf("reader body = %q", payload)
	}

	payload, reader, err = requestBody(nil)
	if err != nil || payload != "" || reader != nil {
		t.Fatalf("nil body = %q reader=%v err=%v", payload, reader, err)
	}
}

func recorder(status int, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.WriteHeader(status)
	_, _ = rec.Write(body)
	return rec
}

func encode(t *testing.T, body any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
