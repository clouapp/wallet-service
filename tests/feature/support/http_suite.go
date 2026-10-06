package support

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"

	"github.com/stretchr/testify/suite"
)

// HTTPSuite is the base an HTTP feature suite embeds. An error path checks
// the status and the body with AssertError.
type HTTPSuite struct {
	suite.Suite
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
