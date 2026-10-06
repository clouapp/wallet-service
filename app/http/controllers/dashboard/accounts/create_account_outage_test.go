package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
)

func TestCreateAccountOutageOmitsTheCause(t *testing.T) {
	cause := errors.New(`create account: pq: insert into "accounts" ("name") values ('Acme')`)
	ctrl := NewAccountsController(AccountsControllerDeps{
		AccountService: accountsvc.NewService(accountsvc.Deps{
			Accounts: createFailsAccounts{err: cause},
		}),
		Passwords:      &authsvc.Service{},
		Limits:         &settings.Service{},
		Features:       &featuressvc.Service{},
		CredentialMail: &credentialmail.Service{},
	})
	response := &recordingResponse{}
	ctrl.CreateAccount(&recordingContext{
		base:     context.WithValue(context.Background(), requestctx.KeyUserID, uuid.New()),
		request:  &recordingRequest{name: "Acme"},
		response: response,
	})

	if response.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.status)
	}
	if bytes.Contains(response.raw, []byte("pq:")) || bytes.Contains(response.raw, []byte("Acme")) || bytes.Contains(response.raw, []byte(cause.Error())) {
		t.Fatalf("response body contains the cause: %s", response.raw)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.raw, &body); err != nil {
		t.Fatal("response body is not the error envelope")
	}
	if body.Error.Code != responses.CodeInternal || body.Error.Message != "internal error" {
		t.Fatalf("response envelope = %+v", body.Error)
	}
}

type createFailsAccounts struct {
	accountsvc.AccountStore
	err error
}

func (a createFailsAccounts) Create(context.Context, *models.Account) error {
	return a.err
}
