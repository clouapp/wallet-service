package wallets

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
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// TestAddWalletUserDeniesWhenTheMembershipReadFails proves a failed lookup of
// an existing wallet membership does not create another row or answer 201.
// A missing row is not this case.
func TestAddWalletUserDeniesWhenTheMembershipReadFails(t *testing.T) {
	previous := foundation.App
	foundation.App = quietWalletApp{}
	t.Cleanup(func() { foundation.App = previous })

	accountID := uuid.New()
	walletID := uuid.New()
	targetID := uuid.New()
	members := &lookupFailsWalletMembers{}
	ctrl := NewUsersController(WalletUsersControllerDeps{
		Members: walletrecords.NewMembers(members),
		Accounts: accountsvc.NewService(accountsvc.Deps{
			Memberships: activeAccountMember{},
		}),
		Memberships: &walletrecords.Memberships{},
	})
	response := &recordingWalletUserResponse{}
	ctx := &recordingWalletUserContext{
		base: context.WithValue(context.Background(), requestctx.KeyWallet, &models.Wallet{
			ID: walletID, AccountID: &accountID, Status: models.StatusActive,
		}),
		request: &recordingWalletUserRequest{
			userID: targetID.String(),
			roles:  models.WalletRoleViewer,
		},
		response: response,
	}

	ctrl.AddWalletUser(ctx)

	if members.created {
		t.Fatal("a failed membership read wrote a wallet user")
	}
	if response.status != http.StatusServiceUnavailable {
		t.Fatalf("failed membership read status = %d, want 503", response.status)
	}
	envelope, ok := response.body.(resources.ErrorEnvelope)
	if !ok {
		t.Fatalf("failed membership read body = %#v, want the error envelope", response.body)
	}
	if envelope.Error.Code != responses.CodeUnavailable || envelope.Error.Message != "failed to load membership" {
		t.Fatalf("failed membership read envelope = %+v", envelope.Error)
	}
}

type lookupFailsWalletMembers struct {
	walletrecords.MemberStore
	created bool
}

func (m *lookupFailsWalletMembers) FindByWalletAndUserIncludeDeleted(context.Context, uuid.UUID, uuid.UUID) (*models.WalletUser, error) {
	return nil, errors.New("membership store unavailable")
}

func (m *lookupFailsWalletMembers) Create(context.Context, *models.WalletUser) error {
	m.created = true
	return nil
}

type activeAccountMember struct {
	accountsvc.MembershipStore
}

func (activeAccountMember) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	return &models.AccountUser{Status: models.MembershipStatusActive}, nil
}

type recordingWalletUserContext struct {
	base     context.Context
	request  *recordingWalletUserRequest
	response *recordingWalletUserResponse
}

func (c *recordingWalletUserContext) Deadline() (time.Time, bool) { return c.base.Deadline() }
func (c *recordingWalletUserContext) Done() <-chan struct{}       { return c.base.Done() }
func (c *recordingWalletUserContext) Err() error                  { return c.base.Err() }
func (c *recordingWalletUserContext) Value(key any) any           { return c.base.Value(key) }
func (c *recordingWalletUserContext) Context() context.Context    { return c.base }
func (c *recordingWalletUserContext) WithContext(ctx context.Context) {
	c.base = ctx
}
func (c *recordingWalletUserContext) WithValue(key any, value any) {
	c.base = context.WithValue(c.base, key, value)
}
func (c *recordingWalletUserContext) Request() http.ContextRequest   { return c.request }
func (c *recordingWalletUserContext) Response() http.ContextResponse { return c.response }

type recordingWalletUserRequest struct {
	http.ContextRequest
	userID string
	roles  string
}

func (r *recordingWalletUserRequest) ValidateRequest(req http.FormRequest) (contractsvalidation.Errors, error) {
	body, ok := req.(*requests.AddWalletUserRequest)
	if !ok {
		return nil, errors.New("unexpected form request")
	}
	body.UserID = r.userID
	body.Roles = r.roles
	return nil, nil
}

type recordingWalletUserResponse struct {
	http.ContextResponse
	status int
	body   any
}

func (r *recordingWalletUserResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.body = obj
	return recordingWalletUserAbort{}
}

type recordingWalletUserAbort struct{}

func (recordingWalletUserAbort) Render() error { return nil }
func (recordingWalletUserAbort) Abort() error  { return nil }

type quietWalletApp struct{ foundationcontract.Application }

func (quietWalletApp) MakeLog() contractslog.Log { return quietWalletLog{} }

type quietWalletLog struct{ contractslog.Log }

func (quietWalletLog) WithContext(context.Context) contractslog.Log { return quietWalletLog{} }
func (quietWalletLog) Errorf(string, ...any)                        {}
