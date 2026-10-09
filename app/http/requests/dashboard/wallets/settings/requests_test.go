package settings_test

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests/dashboard/wallets/settings"
	"github.com/macrowallets/waas/app/services/walletsettings"
)

func TestFreezeRequest_Until(t *testing.T) {
	if (&settings.FreezeRequest{}).Until() != nil {
		t.Fatal("a request without an end names none")
	}

	until := (&settings.FreezeRequest{FrozenUntil: "2026-05-06T07:08:09-03:00"}).Until()

	if until == nil || until.UTC().Format("2006-01-02T15:04:05Z") != "2026-05-06T10:08:09Z" {
		t.Fatalf("until = %v", until)
	}
}

type bodyContext struct{ request *bodyRequest }

func (c bodyContext) Deadline() (time.Time, bool)    { return time.Time{}, false }
func (c bodyContext) Done() <-chan struct{}          { return nil }
func (c bodyContext) Err() error                     { return nil }
func (c bodyContext) Value(any) any                  { return nil }
func (c bodyContext) Context() context.Context       { return context.Background() }
func (c bodyContext) WithContext(context.Context)    {}
func (c bodyContext) WithValue(any, any)             {}
func (c bodyContext) Request() http.ContextRequest   { return c.request }
func (c bodyContext) Response() http.ContextResponse { return nil }

type bodyRequest struct {
	http.ContextRequest
	origin *nethttp.Request
}

func (r *bodyRequest) Origin() *nethttp.Request { return r.origin }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

func TestReadUpdateRequest(t *testing.T) {
	t.Run("reads the body and leaves it readable", func(t *testing.T) {
		origin := &nethttp.Request{Body: io.NopCloser(strings.NewReader(`{"label":"cold"}`))}

		req, err := settings.ReadUpdateRequest(bodyContext{request: &bodyRequest{origin: origin}})

		if err != nil || string(req.Body) != `{"label":"cold"}` {
			t.Fatalf("body = %q, err = %v", req.Body, err)
		}
		again, _ := io.ReadAll(origin.Body)
		if string(again) != `{"label":"cold"}` {
			t.Fatalf("body left unreadable: %q", again)
		}
	})

	t.Run("keeps one byte past the limit so the parser can refuse an oversized body", func(t *testing.T) {
		huge := strings.Repeat("x", walletsettings.MaxBodyBytes*2)
		origin := &nethttp.Request{Body: io.NopCloser(strings.NewReader(huge))}

		req, err := settings.ReadUpdateRequest(bodyContext{request: &bodyRequest{origin: origin}})

		if err != nil || len(req.Body) != walletsettings.MaxBodyBytes+1 {
			t.Fatalf("read %d bytes, err = %v", len(req.Body), err)
		}
	})

	t.Run("no body and an unreadable body are refused", func(t *testing.T) {
		for name, tc := range map[string]struct {
			origin *nethttp.Request
			want   error
		}{
			"no request": {nil, settings.ErrBodyRequired},
			"no body":    {&nethttp.Request{}, settings.ErrBodyRequired},
			"unreadable": {&nethttp.Request{Body: failingBody{}}, settings.ErrBodyUnreadable},
		} {
			_, err := settings.ReadUpdateRequest(bodyContext{request: &bodyRequest{origin: tc.origin}})

			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: err = %v, want %v", name, err, tc.want)
			}
		}
	})
}
