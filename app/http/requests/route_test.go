package requests_test

import (
	"context"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
)

// TestRoute_Param_ReadsTheValueAsItIs pins the reader for a path id that
// is not a UUID (a chain id, a currency code): an empty value is the error
// the handler answers 400 for, and anything else, blank spaces included,
// reaches the lookup untouched, so "   " is the lookup's 404, not a 400.
func TestRoute_Param_ReadsTheValueAsItIs(t *testing.T) {
	for _, value := range []string{"eth", "USD", "   ", " eth "} {
		got, err := requests.RouteParam(routeContext{"chainId": value}, "chainId")
		if err != nil || got != value {
			t.Fatalf("RouteParam(%q) = %q, %v; want the value as it is", value, got, err)
		}
	}

	if got, err := requests.RouteParam(routeContext{}, "chainId"); err == nil || got != "" {
		t.Fatalf("an empty value = %q, %v; want an error", got, err)
	}
	if _, err := requests.RouteParam(routeContext{"chainId": "eth"}, ""); err == nil {
		t.Fatal("a missing parameter name read a value")
	}
	if _, err := requests.RouteParam(nil, "chainId"); err == nil {
		t.Fatal("a missing request read a value")
	}
}

// routeContext is an http.Context whose request carries only path parameters.
type routeContext map[string]string

func (c routeContext) Request() http.ContextRequest { return routeRequest{routes: c} }

func (routeContext) Context() context.Context       { return context.Background() }
func (routeContext) Value(any) any                  { return nil }
func (routeContext) WithContext(context.Context)    {}
func (routeContext) WithValue(any, any)             {}
func (routeContext) Response() http.ContextResponse { return nil }
func (routeContext) Done() <-chan struct{}          { return nil }
func (routeContext) Err() error                     { return nil }
func (routeContext) Deadline() (deadline time.Time, ok bool) {
	return deadline, false
}

type routeRequest struct {
	http.ContextRequest
	routes map[string]string
}

func (r routeRequest) Route(key string) string { return r.routes[key] }
