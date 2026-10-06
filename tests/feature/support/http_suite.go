package support

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"
)

// HTTPSuite is the base an HTTP feature suite embeds. Start it with RunSuite.
// A dashboard request passes its session on the call (s.Get(path, session)).
// An external request passes its token (s.External(path, token)), which signs
// the body when the token requires it. The credential is an argument: the
// suite does not keep one for a request to pick up. An error path checks the
// status and the body with AssertError.
type HTTPSuite struct {
	suite.Suite
	tc goravelTesting.TestCase
}

// Session is the dashboard credential passed on every dashboard request.
// A zero Session sends no Authorization header. AccountID, when set, is the
// X-Account-Id scope header.
type Session struct {
	AccessToken string
	AccountID   string
}

// RunSuite starts an HTTP feature suite.
func RunSuite(t *testing.T, s suite.TestingSuite) {
	t.Helper()
	suite.Run(t, s)
}

// AssertError checks that rec answered with status and the envelope
// {"error":{"code","message"}}. A string error is refused. Extra fields
// inside the error object, and a sibling "errors" map, are left unread.
func (s *HTTPSuite) AssertError(rec *httptest.ResponseRecorder, status int, code, message string) {
	s.T().Helper()
	if err := matchErrorEnvelope(rec, status, code, message); err != nil {
		s.Fail(err.Error())
	}
}

func matchErrorEnvelope(rec *httptest.ResponseRecorder, status int, code, message string) error {
	if rec == nil {
		return fmt.Errorf("nil response")
	}
	if rec.Code != status {
		return fmt.Errorf("status = %d, want %d", rec.Code, status)
	}
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	payload := bytes.TrimSpace(body.Error)
	if len(payload) == 0 || payload[0] != '{' {
		return fmt.Errorf("error is not an object: %s", rec.Body.String())
	}
	var fields struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("error object: %w", err)
	}
	if fields.Code != code {
		return fmt.Errorf("code = %q, want %q", fields.Code, code)
	}
	if fields.Message != message {
		return fmt.Errorf("message = %q, want %q", fields.Message, message)
	}
	return nil
}
