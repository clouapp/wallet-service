package requests

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/validation"
	ginpkg "github.com/goravel/gin"
)

// afterRequest is a form request with a rule and a check on the bound value.
type afterRequest struct {
	Name  string `form:"name"  json:"name"`
	Count int    `form:"count" json:"count"`

	refuse  map[string][]string
	checked bool
}

func (r *afterRequest) Authorize(http.Context) error { return nil }

func (r *afterRequest) Rules(http.Context) map[string]string {
	return map[string]string{"name": "required"}
}

func (r *afterRequest) After(http.Context) map[string][]string {
	r.checked = true
	return r.refuse
}

func validateJSON(t *testing.T, req http.FormRequest, body string) (http.Response, *httptest.ResponseRecorder) {
	t.Helper()
	previous := ginpkg.ValidationFacade
	ginpkg.ValidationFacade = validation.NewValidation()
	t.Cleanup(func() { ginpkg.ValidationFacade = previous })

	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(nethttp.MethodPost, "/", strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	return Validate(ginpkg.NewContext(ginCtx), req), rec
}

func render(t *testing.T, response http.Response, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if response == nil {
		t.Fatal("expected a response")
	}
	if err := response.Render(); err != nil {
		t.Fatal(err)
	}
	rec.Flush()
	return rec.Body.String()
}

func TestValidate_After_AnswersItsFieldsOnceTheRulesPass(t *testing.T) {
	req := &afterRequest{refuse: map[string][]string{"name": {"The name is taken."}}}
	response, rec := validateJSON(t, req, `{"name":"acme"}`)

	body := render(t, response, rec)
	if rec.Code != nethttp.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	const want = `{"error":{"code":"validation_failed","message":"validation failed"},"errors":{"name":["The name is taken."]}}`
	if body != want {
		t.Fatalf("body = %s", body)
	}
	if req.Name != "acme" {
		t.Fatal("the check did not run on the bound request")
	}
}

func TestValidate_After_IsNotAskedWhenARuleFails(t *testing.T) {
	req := &afterRequest{refuse: map[string][]string{"count": {"never"}}}
	response, rec := validateJSON(t, req, `{"count":1}`)

	body := render(t, response, rec)
	if req.checked {
		t.Fatal("the check ran although a rule failed")
	}
	if !strings.Contains(body, `"name"`) || strings.Contains(body, "never") {
		t.Fatalf("body = %s", body)
	}
}

func TestValidate_After_IsNotAskedWhenTheBodyDoesNotBind(t *testing.T) {
	req := &afterRequest{refuse: map[string][]string{"count": {"never"}}}
	response, rec := validateJSON(t, req, `{"name":"acme","count":"many"}`)

	body := render(t, response, rec)
	if req.checked {
		t.Fatal("the check ran although the body did not bind")
	}
	if rec.Code != nethttp.StatusBadRequest || body != `{"error":{"code":"invalid_request","message":"invalid request body"}}` {
		t.Fatalf("status = %d, body = %s", rec.Code, body)
	}
}

func TestValidate_After_LetsTheHandlerGoOnWhenItHasNothingToSay(t *testing.T) {
	req := &afterRequest{}
	response, _ := validateJSON(t, req, `{"name":"acme"}`)
	if response != nil {
		t.Fatal("a passing check answered")
	}
	if !req.checked {
		t.Fatal("the check was not asked")
	}
}
