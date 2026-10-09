package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// TestChild_Lookup_FailureIsUnavailable covers the answer the route matrix
// cannot reach: a member, token or invite read that fails is 503 with the
// child's message, through the legacy writer, and the request stops there.
// An account service without stores fails every read.
func TestChild_Lookup_FailureIsUnavailable(t *testing.T) {
	accounts := accountsvc.NewService(accountsvc.Deps{})
	cases := []struct {
		guard   string
		guarded http.Middleware
		route   string
		message string
	}{
		{"Can member", Can(accounts, PermUsersWrite), "userId", "failed to load membership"},
		{"Can token", Can(accounts, PermTokensWrite), "tokenId", "failed to load token"},
		{"Can invite", Can(accounts, PermUsersWrite), "id", "failed to load invite"},
		{"AccountUpdateMember", AccountUpdateMember(accounts), "userId", "failed to load membership"},
	}
	for _, tc := range cases {
		t.Run(tc.guard, func(t *testing.T) {
			ctx := newGuardContext(map[string]string{tc.route: uuid.NewString()})
			ctx.WithValue(requestctx.KeyAccount, &models.Account{ID: uuid.New()})
			ctx.WithValue(requestctx.KeyAccountRole, models.AccountRoleOwner)

			tc.guarded(ctx)

			ctx.assertRefused(t, http.StatusServiceUnavailable, resources.CodeUnavailable, tc.message)
		})
	}
}

// TestWallet_Can_FollowsTheWalletCatalog pins WalletCan, which no route
// registers today: the stored request grants decide when present, the
// account role's wallet catalog otherwise, and a refusal is 403 forbidden.
// A permission the Gate does not define is refused when the guard is built.
func TestWallet_Can_FollowsTheWalletCatalog(t *testing.T) {
	roles := []string{
		models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleAuditor, models.AccountRoleUser,
		models.RetiredAccountRoleViewer, "", "superuser",
	}
	assertPanics(t, "an empty permission", func() { WalletCan("") })
	for _, permission := range models.AccountPermissions() {
		for _, role := range roles {
			for _, stored := range []bool{false, true} {
				ctx := newGuardContext(nil)
				ctx.WithValue(requestctx.KeyAccountRole, role)
				if stored {
					ctx.WithValue(policies.RequestGrantsKey(), policies.AttachRequestGrants(role))
				}

				WalletCan(permission)(ctx)

				if policies.Can(policies.WalletGrants(role), permission) {
					ctx.assertPassed(t)
					continue
				}
				ctx.assertRefused(t, http.StatusForbidden, resources.CodeForbidden, resources.CodeForbidden)
			}
		}
	}
}

// guardContext is an http.Context that records the one JSON answer a guard
// writes and whether it let the request through.
type guardContext struct {
	base     context.Context
	request  *guardRequest
	response *guardResponse
}

func newGuardContext(routes map[string]string) *guardContext {
	return &guardContext{
		base:     context.WithValue(context.Background(), requestctx.KeyUserID, uuid.New()),
		request:  &guardRequest{routes: routes},
		response: &guardResponse{},
	}
}

func (c *guardContext) assertPassed(t *testing.T) {
	t.Helper()
	if !c.request.next || c.response.written {
		t.Fatalf("want the request let through, next=%t written=%t status=%d body=%#v",
			c.request.next, c.response.written, c.response.status, c.response.body)
	}
}

func (c *guardContext) assertRefused(t *testing.T, status int, code, message string) {
	t.Helper()
	if c.request.next {
		t.Fatal("the refused request went on to the handler")
	}
	if !c.response.aborted {
		t.Fatal("the refusal did not abort the chain")
	}
	if c.response.status != status {
		t.Fatalf("status = %d, want %d", c.response.status, status)
	}
	envelope, ok := c.response.body.(resources.ErrorEnvelope)
	if !ok {
		t.Fatalf("body = %#v, want the error envelope written by ctx.Response().Json", c.response.body)
	}
	if envelope.Error.Code != code || envelope.Error.Message != message {
		t.Fatalf("envelope = %+v, want code %q message %q", envelope.Error, code, message)
	}
}

func (c *guardContext) Deadline() (time.Time, bool)     { return c.base.Deadline() }
func (c *guardContext) Done() <-chan struct{}           { return c.base.Done() }
func (c *guardContext) Err() error                      { return c.base.Err() }
func (c *guardContext) Value(key any) any               { return c.base.Value(key) }
func (c *guardContext) Context() context.Context        { return c.base }
func (c *guardContext) WithContext(ctx context.Context) { c.base = ctx }
func (c *guardContext) WithValue(key any, value any)    { c.base = context.WithValue(c.base, key, value) }
func (c *guardContext) Request() http.ContextRequest    { return c.request }
func (c *guardContext) Response() http.ContextResponse  { return c.response }

type guardRequest struct {
	http.ContextRequest
	routes map[string]string
	next   bool
}

func (r *guardRequest) Route(key string) string { return r.routes[key] }

func (r *guardRequest) Next() { r.next = true }

type guardResponse struct {
	http.ContextResponse
	written bool
	aborted bool
	status  int
	body    any
}

func (r *guardResponse) Json(code int, obj any) http.AbortableResponse {
	r.written = true
	r.status = code
	r.body = obj
	return guardAbort{response: r}
}

type guardAbort struct{ response *guardResponse }

func (a guardAbort) Render() error { return nil }
func (a guardAbort) Abort() error {
	a.response.aborted = true
	return nil
}
