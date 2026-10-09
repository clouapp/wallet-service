package features

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/google/uuid"

	featuressvc "github.com/macrowallets/waas/app/services/features"
)

func routeContext(scope, id string) *ginpkg.Context {
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(nethttp.MethodPut, "/", nil)
	ginCtx.Params = gin.Params{{Key: "scope", Value: scope}, {Key: "id", Value: id}}
	return ginpkg.NewContext(ginCtx)
}

func TestScopeRequest_Decode_RefusesWhatIsNotAnAccountScope(t *testing.T) {
	account := uuid.New().String()
	for name, tc := range map[string]struct {
		scope, id string
		want      error
	}{
		"account":     {featuressvc.ScopeAccount, account, nil},
		"trimmed":     {" account ", " " + account + " ", nil},
		"global":      {"global", account, featuressvc.ErrScopeNotFound},
		"user":        {"user", account, featuressvc.ErrScopeNotFound},
		"empty scope": {"", account, featuressvc.ErrScopeNotFound},
		"not a uuid":  {featuressvc.ScopeAccount, "nope", featuressvc.ErrInvalidAccountID},
		"nil uuid":    {featuressvc.ScopeAccount, uuid.Nil.String(), featuressvc.ErrInvalidAccountID},
		"scope wins":  {"global", "nope", featuressvc.ErrScopeNotFound},
		"empty id":    {featuressvc.ScopeAccount, "", featuressvc.ErrInvalidAccountID},
	} {
		t.Run(name, func(t *testing.T) {
			var req ScopeRequest
			err := req.Decode(routeContext(tc.scope, tc.id))
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && (req.Scope != featuressvc.ScopeAccount || req.ID != account) {
				t.Fatalf("decoded %+v", req)
			}
		})
	}
}
