package wallets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

func TestMapCreateWalletErrorDoesNotHideAnOutageAs409(t *testing.T) {
	cause := errors.New("create wallet: pq: connection refused at db.internal")
	response := &recordingCreateResponse{}
	mapCreateWalletError(&recordingCreateContext{response: response}, cause)

	if response.status != http.StatusInternalServerError {
		t.Fatalf("outage status = %d, want 500", response.status)
	}
	if bytes.Contains(response.raw, []byte("db.internal")) || bytes.Contains(response.raw, []byte("pq:")) {
		t.Fatalf("outage body leaked the cause: %s", response.raw)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.raw, &body); err != nil {
		t.Fatalf("outage body: %v", err)
	}
	if body.Error.Code != responses.CodeInternal || body.Error.Message != "internal error" {
		t.Fatalf("outage envelope = %+v", body.Error)
	}
}

func TestMapCreateWalletErrorKeepsCallerFailuresAs4xx(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "unknown chain",
			err:     fmt.Errorf("%w: sol", chainregistry.ErrUnknownChain),
			status:  http.StatusConflict,
			code:    responses.CodeConflict,
			message: "unknown chain",
		},
		{
			name:    "short passphrase",
			err:     errors.New("passphrase must be at least 12 characters"),
			status:  http.StatusUnprocessableEntity,
			code:    responses.CodeUnprocessable,
			message: "passphrase must be at least 12 characters",
		},
		{
			name:    "missing account",
			err:     errors.New("account_id is required"),
			status:  http.StatusBadRequest,
			code:    responses.CodeInvalidRequest,
			message: "account_id is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := &recordingCreateResponse{}
			mapCreateWalletError(&recordingCreateContext{response: response}, tc.err)
			if response.status != tc.status {
				t.Fatalf("status = %d, want %d", response.status, tc.status)
			}
			envelope, ok := response.body.(resources.ErrorEnvelope)
			if !ok {
				t.Fatalf("body = %#v, want the error envelope", response.body)
			}
			if envelope.Error.Code != tc.code || envelope.Error.Message != tc.message {
				t.Fatalf("envelope = %+v", envelope.Error)
			}
		})
	}
}

type recordingCreateContext struct {
	response *recordingCreateResponse
}

func (c *recordingCreateContext) Deadline() (time.Time, bool)  { return time.Time{}, false }
func (c *recordingCreateContext) Done() <-chan struct{}        { return nil }
func (c *recordingCreateContext) Err() error                   { return nil }
func (c *recordingCreateContext) Value(any) any                { return nil }
func (c *recordingCreateContext) Context() context.Context     { return context.Background() }
func (c *recordingCreateContext) WithContext(context.Context)  {}
func (c *recordingCreateContext) WithValue(any, any)           {}
func (c *recordingCreateContext) Request() http.ContextRequest { return nil }
func (c *recordingCreateContext) Response() http.ContextResponse {
	return c.response
}

type recordingCreateResponse struct {
	http.ContextResponse
	status int
	body   any
	raw    []byte
}

func (r *recordingCreateResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.body = obj
	return recordingCreateAbort{}
}

func (r *recordingCreateResponse) Data(code int, _ string, data []byte) http.AbortableResponse {
	r.status = code
	r.raw = append([]byte(nil), data...)
	return recordingCreateAbort{}
}

type recordingCreateAbort struct{}

func (recordingCreateAbort) Render() error { return nil }
func (recordingCreateAbort) Abort() error  { return nil }
