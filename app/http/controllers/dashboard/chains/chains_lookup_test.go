package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	foundationcontract "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/http"
	contractslog "github.com/goravel/framework/contracts/log"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

type lookupCatalog struct {
	chainsvc.Catalog
	chain *models.Chain
	err   error
}

func (c lookupCatalog) FindByID(context.Context, string) (*models.Chain, error) {
	return c.chain, c.err
}

type recordingContext struct {
	base     context.Context
	request  *recordingRequest
	response *recordingResponse
}

func (c *recordingContext) Deadline() (time.Time, bool)     { return c.base.Deadline() }
func (c *recordingContext) Done() <-chan struct{}           { return c.base.Done() }
func (c *recordingContext) Err() error                      { return c.base.Err() }
func (c *recordingContext) Value(key any) any               { return c.base.Value(key) }
func (c *recordingContext) Context() context.Context        { return c.base }
func (c *recordingContext) WithContext(ctx context.Context) { c.base = ctx }
func (c *recordingContext) WithValue(key any, value any) {
	c.base = context.WithValue(c.base, key, value)
}
func (c *recordingContext) Request() http.ContextRequest   { return c.request }
func (c *recordingContext) Response() http.ContextResponse { return c.response }

type recordingRequest struct {
	http.ContextRequest
	chainID string
}

func (r *recordingRequest) Route(string) string { return r.chainID }

type recordingResponse struct {
	http.ContextResponse
	status int
	raw    []byte
}

func (r *recordingResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.raw, _ = json.Marshal(obj)
	return recordingAbort{}
}

func (r *recordingResponse) Data(code int, _ string, data []byte) http.AbortableResponse {
	r.status = code
	r.raw = append([]byte(nil), data...)
	return recordingAbort{}
}

type recordingAbort struct{}

func (recordingAbort) Render() error { return nil }
func (recordingAbort) Abort() error  { return nil }

type quietApp struct{ foundationcontract.Application }

func (quietApp) MakeLog() contractslog.Log { return quietLog{} }

type quietLog struct{ contractslog.Log }

func (l quietLog) WithContext(context.Context) contractslog.Log { return l }
func (quietLog) Errorf(string, ...any)                          {}

func TestChain_Lookup_DistinguishesNotFoundFromAnOutage(t *testing.T) {
	handlers := map[string]func(*ChainController, http.Context) http.Response{
		"Show":      (*ChainController).Show,
		"Tokens":    (*ChainController).Tokens,
		"Resources": (*ChainController).Resources,
	}
	cases := []struct {
		name       string
		catalog    lookupCatalog
		wantStatus int
		wantCode   string
	}{
		{"missing row", lookupCatalog{err: fmt.Errorf("find chain: %w", models.ErrRepositoryNotFound)}, http.StatusNotFound, responses.CodeNotFound},
		{"nil chain", lookupCatalog{}, http.StatusNotFound, responses.CodeNotFound},
		{"repository error", lookupCatalog{err: errors.New("connection refused")}, http.StatusInternalServerError, responses.CodeInternal},
	}
	previous := foundation.App
	foundation.App = quietApp{}
	t.Cleanup(func() { foundation.App = previous })

	for handlerName, handler := range handlers {
		for _, tc := range cases {
			t.Run(handlerName+"/"+tc.name, func(t *testing.T) {
				ctrl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: tc.catalog}))
				response := &recordingResponse{}
				handler(ctrl, &recordingContext{
					base:     context.Background(),
					request:  &recordingRequest{chainID: "eth"},
					response: response,
				})

				if response.status != tc.wantStatus {
					t.Fatalf("status = %d, want %d", response.status, tc.wantStatus)
				}
				var body resources.ErrorEnvelope
				if err := json.Unmarshal(response.raw, &body); err != nil {
					t.Fatalf("body is not the error envelope: %v", err)
				}
				if body.Error.Code != tc.wantCode {
					t.Fatalf("code = %q, want %q", body.Error.Code, tc.wantCode)
				}
				if strings.Contains(string(response.raw), "connection refused") {
					t.Fatal("the body carries the cause")
				}
			})
		}
	}
}

func TestChain_Lookup_RefusesAChainOfTheOtherNetworkKind(t *testing.T) {
	handlers := map[string]func(*ChainController, http.Context) http.Response{
		"Show":      (*ChainController).Show,
		"Tokens":    (*ChainController).Tokens,
		"Resources": (*ChainController).Resources,
	}
	for handlerName, handler := range handlers {
		t.Run(handlerName, func(t *testing.T) {
			ctrl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: lookupCatalog{chain: &models.Chain{ID: "sepolia", IsTestnet: true}}}))
			response := &recordingResponse{}
			handler(ctrl, &recordingContext{
				base:     context.WithValue(context.Background(), requestctx.KeyAccountEnvironment, models.EnvironmentProd),
				request:  &recordingRequest{chainID: "sepolia"},
				response: response,
			})

			if response.status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.status)
			}
			var body resources.ErrorEnvelope
			if err := json.Unmarshal(response.raw, &body); err != nil {
				t.Fatalf("body is not the error envelope: %v", err)
			}
			if body.Error.Code != responses.CodeForbidden || body.Error.Message != "chain not available in current environment" {
				t.Fatalf("envelope = %+v", body.Error)
			}
		})
	}
}

type failingTokens struct{}

func (failingTokens) FindByChainID(context.Context, string) ([]models.Token, error) {
	return nil, errors.New("tokens store down")
}

type failingResources struct{}

func (failingResources) FindByChainID(context.Context, string) ([]models.ChainResource, error) {
	return nil, errors.New("resources store down")
}

// A failed token or resource read leaves that list null in the chain view instead of failing it.
func TestGet_Chain_ServesTheChainWhenTheSideReadsFail(t *testing.T) {
	ctrl := NewChainController(chainsvc.NewService(chainsvc.Deps{
		Chains:    lookupCatalog{chain: &models.Chain{ID: "eth"}},
		Tokens:    failingTokens{},
		Resources: failingResources{},
	}))
	response := &recordingResponse{}
	ctrl.Show(&recordingContext{
		base:     context.Background(),
		request:  &recordingRequest{chainID: "eth"},
		response: response,
	})

	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	if !strings.Contains(string(response.raw), `"tokens":null`) || !strings.Contains(string(response.raw), `"resources":null`) {
		t.Fatalf("body = %s", response.raw)
	}
}

func (r *recordingResponse) Success() http.ResponseStatus { return successStatus{response: r} }

type successStatus struct {
	http.ResponseStatus
	response *recordingResponse
}

func (s successStatus) Json(obj any) http.AbortableResponse {
	return s.response.Json(http.StatusOK, obj)
}

func TestTokens_And_Resources_AnswerTheirOwnMessageWhenTheirReadFails(t *testing.T) {
	ctrl := NewChainController(chainsvc.NewService(chainsvc.Deps{
		Chains:    lookupCatalog{chain: &models.Chain{ID: "eth"}},
		Tokens:    failingTokens{},
		Resources: failingResources{},
	}))
	cases := map[string]struct {
		handler func(http.Context) http.Response
		message string
	}{
		"Tokens":    {ctrl.Tokens, "failed to fetch tokens"},
		"Resources": {ctrl.Resources, "failed to fetch resources"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := &recordingResponse{}
			tc.handler(&recordingContext{
				base:     context.Background(),
				request:  &recordingRequest{chainID: "eth"},
				response: response,
			})

			if response.status != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", response.status)
			}
			var body resources.ErrorEnvelope
			if err := json.Unmarshal(response.raw, &body); err != nil {
				t.Fatalf("body is not the error envelope: %v", err)
			}
			if body.Error.Code != responses.CodeInternal || body.Error.Message != tc.message {
				t.Fatalf("envelope = %+v", body.Error)
			}
			if strings.Contains(string(response.raw), "store down") {
				t.Fatal("the body carries the cause")
			}
		})
	}
}

// The path segment is used as it arrives: only an empty one is the 400. A blank
// id ("   ") reaches the lookup and answers 404, as it did before the handlers
// moved to the controller standard (requests.RouteString would trim it to "").
func TestChain_Lookup_PassesABlankIDToTheLookup(t *testing.T) {
	handlers := map[string]func(*ChainController, http.Context) http.Response{
		"Show":      (*ChainController).Show,
		"Tokens":    (*ChainController).Tokens,
		"Resources": (*ChainController).Resources,
	}
	previous := foundation.App
	foundation.App = quietApp{}
	t.Cleanup(func() { foundation.App = previous })

	for handlerName, handler := range handlers {
		for _, id := range []string{"", "   "} {
			t.Run(fmt.Sprintf("%s/%q", handlerName, id), func(t *testing.T) {
				ctrl := NewChainController(chainsvc.NewService(chainsvc.Deps{Chains: lookupCatalog{}}))
				response := &recordingResponse{}
				handler(ctrl, &recordingContext{
					base:     context.Background(),
					request:  &recordingRequest{chainID: id},
					response: response,
				})

				want := http.StatusNotFound
				if id == "" {
					want = http.StatusBadRequest
				}
				if response.status != want {
					t.Fatalf("status = %d, want %d", response.status, want)
				}
			})
		}
	}
}
