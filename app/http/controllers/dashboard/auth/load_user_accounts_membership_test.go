package auth

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
	"golang.org/x/crypto/bcrypt"

	authrequests "github.com/macrowallets/waas/app/http/requests/dashboard/auth"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

// TestLoginDeniesWhenTheMembershipReadFails proves a failed membership read
// does not issue a session or return the signed-in body. An empty membership
// list is a successful read and is not this case.
func TestLogin_Denies_WhenTheMembershipReadFails(t *testing.T) {
	previous := foundation.App
	foundation.App = quietApp{}
	t.Cleanup(func() { foundation.App = previous })

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &models.User{
		ID:           uuid.New(),
		Email:        "member@example.com",
		PasswordHash: string(hash),
		Status:       models.StatusActive,
	}
	memberships := &failingMemberships{}
	refreshes := &flagRefreshStore{}
	ctrl := NewAuthController(AuthControllerDeps{
		Users: usersvc.NewService(usersvc.Deps{Store: loginUsers{user: user}}),
		Accounts: accountsvc.NewService(accountsvc.Deps{
			Memberships: memberships,
		}),
		RefreshTokens:  sessions.NewRefreshTokens(refreshes),
		PasswordResets: &sessions.PasswordResets{},
		Passwords:      authsvc.NewService(testHasher()),
		TwoFactor:      &authsvc.TwoFactorLogin{},
		Revoker:        &authsvc.SessionRevoker{},
		CredentialMail: &credentialmail.Service{},
	})
	response := &recordingResponse{}
	ctx := &recordingContext{
		base:     context.Background(),
		request:  &recordingRequest{email: user.Email, password: "correct-password"},
		response: response,
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("login panicked instead of denying the failed membership read: %v", recovered)
		}
	}()
	ctrl.Login(ctx)

	if memberships.reads != 1 {
		t.Fatalf("membership reads = %d, want 1", memberships.reads)
	}
	if refreshes.created {
		t.Fatal("a failed membership read stored a refresh token")
	}
	if response.status != http.StatusServiceUnavailable {
		t.Fatalf("failed membership read status = %d, want 503", response.status)
	}
	envelope, ok := response.body.(resources.ErrorEnvelope)
	if !ok {
		t.Fatalf("failed membership read body = %#v, want the error envelope", response.body)
	}
	if envelope.Error.Code != responses.CodeUnavailable || envelope.Error.Message != "failed to load accounts" {
		t.Fatalf("failed membership read envelope = %+v", envelope.Error)
	}
}

type failingMemberships struct {
	accountsvc.MembershipStore
	reads int
}

func (m *failingMemberships) FindByUserID(context.Context, uuid.UUID) ([]models.AccountUser, error) {
	m.reads++
	return nil, errors.New("membership store unavailable")
}

type loginUsers struct {
	usersvc.Store
	user *models.User
}

func (u loginUsers) FindByEmail(context.Context, string) (*models.User, error) {
	return u.user, nil
}

type flagRefreshStore struct {
	sessions.RefreshStore
	created bool
}

func (s *flagRefreshStore) Create(context.Context, *models.RefreshToken) error {
	s.created = true
	return errors.New("session must not be stored")
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
	email    string
	password string
}

func (r *recordingRequest) ValidateRequest(req http.FormRequest) (contractsvalidation.Errors, error) {
	body, ok := req.(*authrequests.LoginRequest)
	if !ok {
		return nil, errors.New("unexpected form request")
	}
	body.Email = r.email
	body.Password = r.password
	return nil, nil
}

type recordingResponse struct {
	http.ContextResponse
	status int
	body   any
	raw    []byte
}

func (r *recordingResponse) Data(code int, _ string, data []byte) http.AbortableResponse {
	r.status = code
	r.raw = data
	return recordingAbort{}
}

func (r *recordingResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.body = obj
	return recordingAbort{}
}

type recordingAbort struct{}

func (recordingAbort) Render() error { return nil }
func (recordingAbort) Abort() error  { return nil }

type quietApp struct{ foundationcontract.Application }

func (quietApp) MakeLog() contractslog.Log { return quietLog{} }

type quietLog struct{ contractslog.Log }

func (quietLog) Errorf(string, ...any) {}

func (l quietLog) WithContext(context.Context) contractslog.Log { return l }
