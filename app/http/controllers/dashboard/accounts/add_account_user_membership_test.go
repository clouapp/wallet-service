package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	foundationcontract "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/http"
	contractslog "github.com/goravel/framework/contracts/log"
	contractsvalidation "github.com/goravel/framework/contracts/validation"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
)

// TestAddAccountUserDeniesWhenTheMembershipReadFails proves a failed read of
// the membership just written does not answer 201. A missing row stays the
// created response; this store error is not that answer.
func TestAdd_Account_UserDeniesWhenTheMembershipReadFails(t *testing.T) {
	previous := foundation.App
	foundation.App = quietApp{}
	t.Cleanup(func() { foundation.App = previous })

	accountID := uuid.New()
	callerID := uuid.New()
	targetID := uuid.New()
	memberships := &readFailsMemberships{actor: &models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: callerID, Role: models.AccountRoleOwner, Status: models.MembershipStatusActive,
	}}
	ctrl := NewAccountsController(AccountsControllerDeps{
		AccountService: accountsvc.NewService(accountsvc.Deps{
			Memberships: memberships,
			Users: emailUsers{user: &models.User{
				ID: targetID, Email: "member@example.com",
			}},
		}),
		Passwords: &authsvc.Service{},
		Limits:    &settings.Service{},
		Features:  &featuressvc.Service{},
	})
	response := &recordingResponse{}
	ctx := &recordingContext{
		base: context.WithValue(
			context.WithValue(context.Background(), requestctx.KeyAccount, &models.Account{
				ID: accountID, Name: "Acme", Status: models.StatusActive,
			}),
			requestctx.KeyUserID,
			callerID,
		),
		request:  &recordingRequest{email: "member@example.com", role: models.AccountRoleUser},
		response: response,
	}

	ctrl.AddAccountUser(ctx)

	if !memberships.created {
		t.Fatal("the membership write did not run")
	}
	if response.status != http.StatusForbidden {
		t.Fatalf("failed membership read status = %d, want 403", response.status)
	}
	envelope, ok := response.body.(resources.ErrorEnvelope)
	if !ok {
		t.Fatalf("failed membership read body = %#v, want the error envelope", response.body)
	}
	if envelope.Error.Code != responses.CodeForbidden || envelope.Error.Message != "not a member of this account" {
		t.Fatalf("failed membership read envelope = %+v", envelope.Error)
	}
}

type readFailsMemberships struct {
	accountsvc.MembershipStore
	actor   *models.AccountUser
	reads   int
	created bool
}

func (m *readFailsMemberships) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	m.reads++
	if m.reads == 1 {
		return m.actor, nil
	}
	return nil, errors.New("membership store unavailable")
}

func (m *readFailsMemberships) FindByAccountAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return nil, models.ErrRepositoryNotFound
}

func (m *readFailsMemberships) Create(context.Context, *models.AccountUser) error {
	m.created = true
	return nil
}

type emailUsers struct {
	accountsvc.UserStore
	user *models.User
}

func (u emailUsers) FindByEmail(context.Context, string) (*models.User, error) {
	return u.user, nil
}

type recordingContext struct {
	base     context.Context
	request  *recordingRequest
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
func (c *recordingContext) Request() http.ContextRequest   { return c.request }
func (c *recordingContext) Response() http.ContextResponse { return c.response }

type recordingRequest struct {
	http.ContextRequest
	email string
	role  string
	name  string
}

func (r *recordingRequest) ValidateRequest(req http.FormRequest) (contractsvalidation.Errors, error) {
	switch body := req.(type) {
	case *requests.AddAccountUserRequest:
		body.Email = r.email
		body.Role = r.role
		return nil, nil
	case *requests.CreateAccountRequest:
		body.Name = r.name
		return nil, nil
	default:
		return nil, errors.New("unexpected form request")
	}
}

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

type recordingAbort struct{}

func (recordingAbort) Render() error { return nil }
func (recordingAbort) Abort() error  { return nil }

type quietApp struct{ foundationcontract.Application }

func (quietApp) MakeLog() contractslog.Log { return quietLog{} }

type quietLog struct{ contractslog.Log }

func (quietLog) WithContext(context.Context) contractslog.Log { return quietLog{} }
func (quietLog) Errorf(string, ...any)                        {}
