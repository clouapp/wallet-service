package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// TestWalletContextDeniesWhenTheMembershipReadFails proves a failed account
// membership read does not continue into the wallet. A wallet membership that
// would otherwise admit the caller is not this case, and a missing membership
// row stays the existing not-found answer.
func TestWallet_Context_DeniesWhenTheMembershipReadFails(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	walletID := uuid.New()
	memberships := &failingAccountMembers{}
	walletMembers := &admittingWalletMembers{}
	bindContainer(t, walletrecords.NewWallets(oneWallet{wallet: &models.Wallet{
		ID:        walletID,
		AccountID: &accountID,
		Status:    models.StatusActive,
	}}))
	bindContainer(t, accountsvc.NewService(accountsvc.Deps{
		Accounts:    openAccount{account: &models.Account{ID: accountID, Status: models.StatusActive}},
		Memberships: memberships,
	}))
	bindContainer(t, walletrecords.NewMembers(walletMembers))

	response := &recordingWalletResponse{}
	request := &recordingWalletRequest{walletID: walletID.String()}
	ctx := &recordingWalletContext{
		base:     context.WithValue(context.Background(), requestctx.KeyUserID, userID),
		request:  request,
		response: response,
	}

	WalletContext()(ctx)

	if memberships.reads != 1 {
		t.Fatalf("membership reads = %d, want 1", memberships.reads)
	}
	if walletMembers.reads != 0 {
		t.Fatal("a failed membership read continued into the wallet membership")
	}
	if request.continued {
		t.Fatal("a failed membership read admitted the request")
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
	if _, stored := requestctx.Wallet(ctx); stored {
		t.Fatal("a failed membership read stored the wallet")
	}
}

func bindContainer[T any](t *testing.T, value T) {
	t.Helper()
	var zero T
	foundation.App.Singleton(zero, func(contractsfoundation.Application) (any, error) {
		return value, nil
	})
	if _, err := container.Make[T](); err != nil {
		t.Fatalf("bind %T: %v", zero, err)
	}
}

type failingAccountMembers struct {
	accountsvc.MembershipStore
	reads int
}

func (m *failingAccountMembers) FindByAccountAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.AccountUser, error) {
	m.reads++
	return nil, errors.New("membership store unavailable")
}

type openAccount struct {
	accountsvc.AccountStore
	account *models.Account
}

func (a openAccount) FindByID(context.Context, uuid.UUID) (*models.Account, error) {
	return a.account, nil
}

type oneWallet struct {
	walletrecords.WalletStore
	wallet *models.Wallet
}

func (w oneWallet) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return w.wallet, nil
}

type admittingWalletMembers struct {
	walletrecords.MemberStore
	reads int
}

func (m *admittingWalletMembers) FindByWalletAndUser(context.Context, uuid.UUID, uuid.UUID) (*models.WalletUser, error) {
	m.reads++
	return &models.WalletUser{ID: uuid.New(), Status: models.StatusActive}, nil
}

type recordingWalletContext struct {
	base     context.Context
	request  *recordingWalletRequest
	response *recordingWalletResponse
}

func (c *recordingWalletContext) Deadline() (time.Time, bool) { return c.base.Deadline() }
func (c *recordingWalletContext) Done() <-chan struct{}       { return c.base.Done() }
func (c *recordingWalletContext) Err() error                  { return c.base.Err() }
func (c *recordingWalletContext) Value(key any) any           { return c.base.Value(key) }
func (c *recordingWalletContext) Context() context.Context    { return c.base }
func (c *recordingWalletContext) WithContext(ctx context.Context) {
	c.base = ctx
}
func (c *recordingWalletContext) WithValue(key any, value any) {
	c.base = context.WithValue(c.base, key, value)
}
func (c *recordingWalletContext) Request() http.ContextRequest   { return c.request }
func (c *recordingWalletContext) Response() http.ContextResponse { return c.response }

type recordingWalletRequest struct {
	http.ContextRequest
	walletID  string
	continued bool
}

func (r *recordingWalletRequest) Route(key string) string {
	if key == "walletId" {
		return r.walletID
	}
	return ""
}

func (r *recordingWalletRequest) Method() string { return http.MethodGet }

func (r *recordingWalletRequest) Next() { r.continued = true }

type recordingWalletResponse struct {
	http.ContextResponse
	status int
	body   any
}

func (r *recordingWalletResponse) Json(code int, obj any) http.AbortableResponse {
	r.status = code
	r.body = obj
	return recordingWalletAbort{}
}

type recordingWalletAbort struct{}

func (recordingWalletAbort) Render() error { return nil }
func (recordingWalletAbort) Abort() error  { return nil }
