package withdrawalrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
)

type workflowSpy struct{ calls int }

func (s *workflowSpy) Inspect(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, recovery.FeeEnvelope, *recovery.GasSeedPlan, string) (*recovery.Proposal, error) {
	s.calls++
	return &recovery.Proposal{}, nil
}
func (s *workflowSpy) Apply(context.Context, uuid.UUID, uuid.UUID, recovery.Proposal, recovery.Review, string, string) (*recovery.Result, error) {
	s.calls++
	return &recovery.Result{}, nil
}
func (s *workflowSpy) Resume(context.Context, uuid.UUID, uuid.UUID, string, string) (*recovery.Result, error) {
	s.calls++
	return &recovery.Result{}, nil
}
func (s *workflowSpy) Lookup(context.Context, uuid.UUID, uuid.UUID) (*recovery.Result, error) {
	s.calls++
	return &recovery.Result{}, nil
}

func recoveryHTTPContext(t *testing.T, account, walletAccount uuid.UUID, environment string, operation uuid.UUID, body string) (http.Context, *httptest.ResponseRecorder, *models.Wallet) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(recorder)
	g.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	g.Request.Header.Set("Content-Type", "application/json")
	g.Params = gin.Params{{Key: "idempotencyKey", Value: operation.String()}}
	ctx := ginpkg.NewContext(g)
	wallet := &models.Wallet{ID: uuid.New(), AccountID: &walletAccount}
	ctx.WithValue(requestctx.KeyWallet, wallet)
	ctx.WithValue(requestctx.KeyAccountID, account)
	ctx.WithValue(requestctx.KeyAccountEnvironment, environment)
	ctx.WithValue(requestctx.KeyAPIToken, &models.AccessToken{ID: uuid.New(), AccountID: account, SpendingLimit: models.NoSpendingLimit})
	return ctx, recorder, wallet
}

func TestRecoveryHTTP_ProductionOrForeignAccountCannotReachAnyWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		environment string
		foreign     bool
	}{
		{name: "production", environment: models.EnvironmentProd},
		{name: "missing environment"},
		{name: "foreign account", environment: models.EnvironmentTest, foreign: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &workflowSpy{}
			controller := New(spy)
			for _, action := range []func(http.Context) http.Response{controller.Inspect, controller.Apply, controller.Resume, controller.Show} {
				account, walletAccount := uuid.New(), uuid.New()
				if !tc.foreign {
					walletAccount = account
				}
				ctx, recorder, _ := recoveryHTTPContext(t, account, walletAccount, tc.environment, uuid.New(), `{}`)
				if err := action(ctx).Render(); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != http.StatusForbidden || spy.calls != 0 {
					t.Fatalf("status=%d workflow calls=%d", recorder.Code, spy.calls)
				}
			}
		})
	}
}

func TestRecoveryHTTP_ForeignOperationBodyIsRefusedBeforeApply(t *testing.T) {
	for _, changed := range []string{"operation", "wallet", "account"} {
		t.Run(changed, func(t *testing.T) {
			account, operation := uuid.New(), uuid.New()
			ctx, recorder, wallet := recoveryHTTPContext(t, account, account, models.EnvironmentTest, operation, "")
			proposal := recovery.Proposal{Binding: recovery.Binding{OperationID: operation, WalletID: wallet.ID, AccountID: account}}
			switch changed {
			case "operation":
				proposal.Binding.OperationID = uuid.New()
			case "wallet":
				proposal.Binding.WalletID = uuid.New()
			case "account":
				proposal.Binding.AccountID = uuid.New()
			}
			encoded, err := json.Marshal(applyRequest{Proposal: proposal, Passphrase: "local-fixture-passphrase"})
			if err != nil {
				t.Fatal(err)
			}
			ctx.Request().Origin().Body = httptest.NewRequest("POST", "/", strings.NewReader(string(encoded))).Body
			ctx.Request().Origin().ContentLength = int64(len(encoded))
			spy := &workflowSpy{}
			if err := New(spy).Apply(ctx).Render(); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusNotFound || spy.calls != 0 {
				t.Fatalf("status=%d workflow calls=%d body=%s", recorder.Code, spy.calls, recorder.Body.String())
			}
		})
	}
}

func TestRecoveryHTTP_InternalErrorsNeverExposeSensitiveCause(t *testing.T) {
	account := uuid.New()
	ctx, recorder, _ := recoveryHTTPContext(t, account, account, models.EnvironmentTest, uuid.New(), "")
	if err := failure(ctx, errors.New("private RPC credential and sealed bytes")).Render(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "credential") || strings.Contains(recorder.Body.String(), "sealed") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRecoveryHTTP_LimitedTokenCannotBypassItsOrdinarySpendingReservation(t *testing.T) {
	for _, limit := range []string{`{"daily_usd":"1"}`, `null`, `{broken`} {
		for _, resume := range []bool{false, true} {
			account := uuid.New()
			ctx, recorder, _ := recoveryHTTPContext(t, account, account, models.EnvironmentTest, uuid.New(), `{}`)
			ctx.WithValue(requestctx.KeyAPIToken, &models.AccessToken{AccountID: account, SpendingLimit: limit})
			spy := &workflowSpy{}
			controller := New(spy)
			action := controller.Apply
			if resume {
				action = controller.Resume
			}
			if err := action(ctx).Render(); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusForbidden || spy.calls != 0 {
				t.Fatalf("limit=%s status=%d calls=%d", limit, recorder.Code, spy.calls)
			}
		}
	}
}
