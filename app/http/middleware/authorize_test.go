package middleware

import (
	"context"
	"testing"

	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/policies"
)

// TestAuthorize_AsksTheGateUnlessTheSubjectEndedIt covers the one guard the
// permission middlewares share: a subject that passes leaves the request to
// the handler, one that answered stops it, and otherwise the Gate decides. A
// refusal is 403 with the ability's sentence through the legacy writer.
func TestAuthorize_AsksTheGateUnlessTheSubjectEndedIt(t *testing.T) {
	gate := access.NewGate(context.Background())
	policies.DefineGates(gate)
	ownerArguments := map[string]any{policies.ArgAccountRole: "owner"}
	userArguments := map[string]any{policies.ArgAccountRole: "user"}

	allowed := newGuardContext(nil)
	authorize(gate, policies.AbilityAccountViewSettings, fixedSubject(ownerArguments, decide))(allowed)
	allowed.assertPassed(t)

	refused := newGuardContext(nil)
	authorize(gate, policies.AbilityAccountViewSettings, fixedSubject(userArguments, decide))(refused)
	refused.assertRefused(t, http.StatusForbidden, resources.CodeForbidden, policies.MsgSettingsViewDenied)

	passed := newGuardContext(nil)
	authorize(gate, policies.AbilityAccountViewSettings, fixedSubject(userArguments, pass))(passed)
	passed.assertPassed(t)

	answeredCtx := newGuardContext(nil)
	authorize(gate, policies.AbilityAccountViewSettings, fixedSubject(ownerArguments, answered))(answeredCtx)
	if answeredCtx.request.next || answeredCtx.response.written {
		t.Fatal("an answered subject let the guard write or continue")
	}
}

// TestAuthorize_RefusesToBuildWithoutADefinedAbility keeps the Gate's
// "ability doesn't exist" sentence out of every body: the guard is not built.
func TestAuthorize_RefusesToBuildWithoutADefinedAbility(t *testing.T) {
	gate := access.NewGate(context.Background())
	assertPanics(t, "an unknown ability", func() { authorize(gate, "nope", fixedSubject(nil, decide)) })
	assertPanics(t, "a nil gate", func() {
		var missing contractsaccess.Gate
		authorize(missing, policies.AbilityAccountViewSettings, fixedSubject(nil, decide))
	})
}

func fixedSubject(arguments map[string]any, result outcome) subject {
	return func(http.Context) (map[string]any, outcome) { return arguments, result }
}

func assertPanics(t *testing.T, name string, build func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: authorize built a guard", name)
		}
	}()
	build()
}
