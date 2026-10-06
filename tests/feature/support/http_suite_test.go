package support

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/resources"
)

func TestHTTP_Suite_AssertError(t *testing.T) {
	suite.Run(t, new(assertErrorSuite))
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
