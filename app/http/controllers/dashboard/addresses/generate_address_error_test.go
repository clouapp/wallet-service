package addresses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/wallet"
)

func TestGenerate_Address_ErrorOmitsTheProviderText(t *testing.T) {
	const upstream = "api error AccessDeniedException: not authorized for arn:aws:secretsmanager:us-east-1:0:secret:share-b-AbCdEf"
	cause := fmt.Errorf("fetch service share: %w", stubProviderError{text: upstream})
	response := &recordingResponse{}
	controllers.AddressGenerationError(&recordingContext{base: context.Background(), response: response}, cause)

	if response.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.status)
	}
	raw := response.bytes(t)
	if bytes.Contains(raw, []byte("AccessDeniedException")) || bytes.Contains(raw, []byte("share-b")) || bytes.Contains(raw, []byte(cause.Error())) {
		t.Fatal("response body contains the provider text")
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal("response body is not the error envelope")
	}
	if body.Error.Code != responses.CodeProviderUnavailable || body.Error.Message != "provider unavailable" {
		t.Fatal("response envelope is not the fixed provider message")
	}
}

func TestGenerate_Address_ErrorKeepsTheHandlersOwnMessage(t *testing.T) {
	response := &recordingResponse{}
	controllers.AddressGenerationError(&recordingContext{base: context.Background(), response: response}, wallet.ErrWalletNotFound)

	if response.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.status)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.bytes(t), &body); err != nil {
		t.Fatal("response body is not the error envelope")
	}
	if body.Error.Code != responses.CodeUnprocessable || body.Error.Message != "wallet not found" {
		t.Fatalf("response envelope = %+v", body.Error)
	}
}

func TestGenerate_Address_ErrorHidesAWrappedCause(t *testing.T) {
	const query = `pq: insert into addresses (label) values ('secret-label')`
	cause := fmt.Errorf("create address: %w", fmt.Errorf("%s", query))
	response := &recordingResponse{}
	controllers.AddressGenerationError(&recordingContext{base: context.Background(), response: response}, cause)

	if response.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.status)
	}
	raw := response.bytes(t)
	if bytes.Contains(raw, []byte("secret-label")) || bytes.Contains(raw, []byte("insert into")) || bytes.Contains(raw, []byte(cause.Error())) {
		t.Fatal("response body contains the wrapped cause")
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal("response body is not the error envelope")
	}
	if body.Error.Code != responses.CodeInternal || body.Error.Message != "internal error" {
		t.Fatalf("response envelope = %+v", body.Error)
	}
}

type stubProviderError struct{ text string }

func (e stubProviderError) Error() string     { return e.text }
func (e stubProviderError) ErrorCode() string { return "AccessDeniedException" }

type recordingContext struct {
	base     context.Context
	response *recordingResponse
}

func (c *recordingContext) Deadline() (time.Time, bool) { return c.base.Deadline() }
func (c *recordingContext) Done() <-chan struct{}       { return c.base.Done() }
func (c *recordingContext) Err() error                  { return c.base.Err() }
func (c *recordingContext) Value(key any) any           { return c.base.Value(key) }
func (c *recordingContext) Context() context.Context    { return c.base }
func (c *recordingContext) WithContext(ctx context.Context) {
	c.base = ctx
}
func (c *recordingContext) WithValue(key any, value any) {
	c.base = context.WithValue(c.base, key, value)
}
func (c *recordingContext) Request() http.ContextRequest   { return nil }
func (c *recordingContext) Response() http.ContextResponse { return c.response }

type recordingResponse struct {
	http.ContextResponse
	status int
	body   any
	raw    []byte
}

func (r *recordingResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.body = obj
	return recordingAbort{}
}

func (r *recordingResponse) Data(code int, _ string, data []byte) http.AbortableResponse {
	r.status = code
	r.raw = append([]byte(nil), data...)
	return recordingAbort{}
}

func (r *recordingResponse) bytes(t *testing.T) []byte {
	t.Helper()
	if len(r.raw) > 0 {
		return r.raw
	}
	encoded, err := json.Marshal(r.body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type recordingAbort struct{}

func (recordingAbort) Render() error { return nil }
func (recordingAbort) Abort() error  { return nil }
