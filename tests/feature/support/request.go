package support

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
)

// Get sends a dashboard GET. session is the credential for this request.
func (s *HTTPSuite) Get(path string, session Session) contractstestinghttp.Response {
	s.T().Helper()
	return s.do(http.MethodGet, path, sessionCredential(session), nil, false)
}

// Post sends a dashboard POST. session is the credential for this request.
func (s *HTTPSuite) Post(path string, session Session, body any) contractstestinghttp.Response {
	s.T().Helper()
	return s.do(http.MethodPost, path, sessionCredential(session), body, true)
}

// Put sends a dashboard PUT. session is the credential for this request.
func (s *HTTPSuite) Put(path string, session Session, body any) contractstestinghttp.Response {
	s.T().Helper()
	return s.do(http.MethodPut, path, sessionCredential(session), body, true)
}

// Patch sends a dashboard PATCH. session is the credential for this request.
func (s *HTTPSuite) Patch(path string, session Session, body any) contractstestinghttp.Response {
	s.T().Helper()
	return s.do(http.MethodPatch, path, sessionCredential(session), body, true)
}

// Delete sends a dashboard DELETE. session is the credential for this request.
func (s *HTTPSuite) Delete(path string, session Session, body any) contractstestinghttp.Response {
	s.T().Helper()
	return s.do(http.MethodDelete, path, sessionCredential(session), body, true)
}

// ExternalRequest is one external API call. Its verb methods sign the body
// when the token carries a signer.
type ExternalRequest struct {
	suite *HTTPSuite
	path  string
	token Token
}

// External starts an external API request. token is the credential for this
// request. Verb methods sign the body when token.Sign is set.
func (s *HTTPSuite) External(path string, token Token) ExternalRequest {
	return ExternalRequest{suite: s, path: path, token: token}
}

// Get sends the external GET.
func (r ExternalRequest) Get() contractstestinghttp.Response {
	r.suite.T().Helper()
	return r.suite.do(http.MethodGet, r.path, tokenCredential(r.token), nil, true)
}

// Post sends the external POST and signs body when the token requires it.
func (r ExternalRequest) Post(body any) contractstestinghttp.Response {
	r.suite.T().Helper()
	return r.suite.do(http.MethodPost, r.path, tokenCredential(r.token), body, true)
}

// Put sends the external PUT and signs body when the token requires it.
func (r ExternalRequest) Put(body any) contractstestinghttp.Response {
	r.suite.T().Helper()
	return r.suite.do(http.MethodPut, r.path, tokenCredential(r.token), body, true)
}

// Patch sends the external PATCH and signs body when the token requires it.
func (r ExternalRequest) Patch(body any) contractstestinghttp.Response {
	r.suite.T().Helper()
	return r.suite.do(http.MethodPatch, r.path, tokenCredential(r.token), body, true)
}

// Delete sends the external DELETE and signs body when the token requires it.
func (r ExternalRequest) Delete(body any) contractstestinghttp.Response {
	r.suite.T().Helper()
	return r.suite.do(http.MethodDelete, r.path, tokenCredential(r.token), body, true)
}

type credential struct {
	bearer    string
	accountID string
	sign      SignFunc
}

func sessionCredential(session Session) credential {
	return credential{bearer: session.AccessToken, accountID: session.AccountID}
}

func tokenCredential(token Token) credential {
	return credential{bearer: token.Bearer, sign: token.Sign}
}

func (s *HTTPSuite) do(method, path string, cred credential, body any, contentType bool) contractstestinghttp.Response {
	s.T().Helper()
	payload, reader, err := requestBody(body)
	if err != nil {
		s.T().Fatalf("%s %s: %v", method, path, err)
	}
	req := s.tc.Http(s.T())
	for key, value := range credentialHeaders(cred, payload, contentType, method != http.MethodGet && method != http.MethodHead) {
		req = req.WithHeader(key, value)
	}
	var resp contractstestinghttp.Response
	switch method {
	case http.MethodGet:
		resp, err = req.Get(path)
	case http.MethodPost:
		resp, err = req.Post(path, reader)
	case http.MethodPut:
		resp, err = req.Put(path, reader)
	case http.MethodPatch:
		resp, err = req.Patch(path, reader)
	case http.MethodDelete:
		resp, err = req.Delete(path, reader)
	default:
		s.T().Fatalf("unsupported method %s", method)
	}
	if err != nil {
		s.T().Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// credentialHeaders builds the auth headers for one request. includeSignature
// is false for verbs that have no body. A signer is applied to payload, the
// exact bytes that will be sent.
func credentialHeaders(cred credential, payload string, contentType, includeSignature bool) map[string]string {
	headers := map[string]string{}
	if contentType {
		headers["Content-Type"] = "application/json"
	}
	if cred.bearer != "" {
		headers["Authorization"] = "Bearer " + cred.bearer
	}
	if cred.accountID != "" {
		headers["X-Account-Id"] = cred.accountID
	}
	if includeSignature && cred.sign != nil {
		headers["X-Signature"] = cred.sign([]byte(payload))
	}
	return headers
}

// requestBody is the JSON this request sends. A nil body sends no reader.
// A string or byte slice is sent verbatim so a signature covers those bytes.
// A reader is buffered once so the signature and the body match.
func requestBody(body any) (string, io.Reader, error) {
	if body == nil {
		return "", nil, nil
	}
	switch v := body.(type) {
	case string:
		return v, strings.NewReader(v), nil
	case []byte:
		return string(v), bytes.NewReader(v), nil
	case io.Reader:
		if v == nil {
			return "", nil, nil
		}
		buf, err := io.ReadAll(v)
		if err != nil {
			return "", nil, err
		}
		return string(buf), bytes.NewReader(buf), nil
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", nil, err
	}
	return string(buf), bytes.NewReader(buf), nil
}
