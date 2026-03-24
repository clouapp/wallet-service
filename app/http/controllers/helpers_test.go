package controllers_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"
)

// testAPISecret is the HMAC secret used for all controller tests.
const testAPISecret = "test-api-secret-for-unit-tests"

// authSuite embeds suite.Suite and goravelTesting.TestCase, and provides
// HMAC-signed HTTP helpers for testing authenticated API endpoints.
type authSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func (s *authSuite) signHeaders(method, fullPath, body string) map[string]string {
	// Strip query string — middleware uses ctx.Request().Path() which has no query
	path := strings.SplitN(fullPath, "?", 2)[0]
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	message := fmt.Sprintf("%s%s%s%s", ts, method, path, body)
	mac := hmac.New(sha256.New, []byte(testAPISecret))
	mac.Write([]byte(message))
	sig := hex.EncodeToString(mac.Sum(nil))
	return map[string]string{
		"X-API-Key":       testAPISecret,
		"X-API-Timestamp": ts,
		"X-API-Signature": sig,
	}
}

// SignedGet performs an authenticated GET request against the test server.
func (s *authSuite) SignedGet(path string) contractstestinghttp.Response {
	headers := s.signHeaders("GET", path, "")
	resp, err := s.Http(s.T()).WithHeaders(headers).Get(path)
	s.Require().NoError(err)
	return resp
}

// SignedPost performs an authenticated POST request with a JSON body.
func (s *authSuite) SignedPost(path, body string) contractstestinghttp.Response {
	headers := s.signHeaders("POST", path, body)
	resp, err := s.Http(s.T()).
		WithHeaders(headers).
		WithHeader("Content-Type", "application/json").
		Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

// toReader converts a string into an io.Reader for HTTP request bodies.
func toReader(s string) io.Reader {
	return strings.NewReader(s)
}
