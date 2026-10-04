package support

import (
	"encoding/json"
	"strings"
	"testing"

	contractstesting "github.com/goravel/framework/contracts/testing"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
)

// Dispatcher is the minimal surface of goravel's testing.TestCase that our
// request helpers need. A test suite embedding goravelTesting.TestCase
// satisfies this by providing the Http method — callers typically pass
// `&s.TestCase`.
//
// Accepting an interface keeps helpers callable from any dispatcher without
// hard-coding the Goravel struct type, and keeps this package import-light
// for any future test harness.
type Dispatcher interface {
	Http(t contractstesting.TestingT) contractstestinghttp.Request
}

// marshalBody converts `body` into the JSON string the Goravel router expects.
//   - string  → used verbatim (lets callers pass hand-written JSON fixtures).
//   - []byte  → used verbatim (lets callers reuse pre-built payloads).
//   - nil     → empty body.
//   - any     → json.Marshal.
//
// Returning a string (and not a reader) is intentional: the same bytes need to
// be signed before being sent, and re-reading an io.Reader is awkward.
func marshalBody(t *testing.T, body any) string {
	t.Helper()
	if body == nil {
		return ""
	}
	switch v := body.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(b)
}

// applyAuth sets the Bearer token and (optionally) the HMAC signature header
// on the given request, returning the decorated request. The body string is
// what gets hashed — callers must pass the exact bytes they'll send.
func applyAuth(req contractstestinghttp.Request, bearer, body string, sign SignFunc) contractstestinghttp.Request {
	req = req.WithHeader("Content-Type", "application/json")
	if bearer != "" {
		req = req.WithHeader("Authorization", "Bearer "+bearer)
	}
	if sign != nil {
		req = req.WithHeader("X-Signature", sign([]byte(body)))
	}
	return req
}

// Post dispatches a JSON POST through the production Goravel router using the
// same s.Http(s.T()) pattern as critical_api_endpoints_test.go. When sign is
// non-nil (i.e. the access token was minted with require_signature=true), the
// X-Signature header is attached with HMAC-SHA256(body) keyed by the raw JWT.
//
// Returns the Goravel test Response so callers can chain AssertStatus /
// AssertJson / Content() in the usual way.
func Post(t *testing.T, d Dispatcher, path string, body any, bearer string, sign SignFunc) contractstestinghttp.Response {
	t.Helper()
	payload := marshalBody(t, body)
	req := applyAuth(d.Http(t), bearer, payload, sign)
	resp, err := req.Post(path, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// Get dispatches a JSON GET through the Goravel router. GETs don't carry a
// body, so no SignFunc is accepted — the external API's HMAC scheme applies
// to request bodies only, and GETs signed with an empty body are not a case
// any current test exercises. Add a SignedGet helper if/when that changes.
func Get(t *testing.T, d Dispatcher, path, bearer string) contractstestinghttp.Response {
	t.Helper()
	req := applyAuth(d.Http(t), bearer, "", nil)
	resp, err := req.Get(path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}
