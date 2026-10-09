package withdrawals

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
)

func TestStoreRequest_Accepts_OnlyThePassphraseField(t *testing.T) {
	rules := (&StoreRequest{}).Rules(scopeContext{ctx: context.Background()})
	if _, ok := rules["passphrase"]; !ok {
		t.Fatal("passphrase is not a form field")
	}
	for _, alias := range []string{"confirm_passphrase", "password", "wallet_password"} {
		if _, ok := rules[alias]; ok {
			t.Fatalf("form still reads %s", alias)
		}
	}
}

func TestStoreRequest_Requires_TheTotpCodeOnlyFromADashboardUser(t *testing.T) {
	token := scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyAccountID, uuid.New())}
	if _, ok := (&StoreRequest{}).Rules(token)["totp_code"]; ok {
		t.Fatal("an access-token caller was asked for a TOTP code")
	}
	session := scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyUserID, uuid.New())}
	if got := (&StoreRequest{}).Rules(session)["totp_code"]; got != "required|min_len:6|max_len:6" {
		t.Fatalf("dashboard totp rule = %q", got)
	}
}

type preparer interface {
	PrepareForValidation(ctx http.Context, data validation.Data) error
}

func TestPrepareForValidation_ReadsTheWalletChainFromTheRequest(t *testing.T) {
	ctx := scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyWallet, &models.Wallet{Chain: "eth"})}
	for _, request := range []preparer{&StoreRequest{}, &EstimateRequest{}} {
		data := chainData{}
		if err := request.PrepareForValidation(ctx, data); err != nil {
			t.Fatalf("%T: %v", request, err)
		}
		if got, ok := data.Get("_chain"); !ok || got != "eth" {
			t.Fatalf("%T chain = %v, %v", request, got, ok)
		}
	}
}

func TestPrepareForValidation_LeavesTheChainUnsetWithoutAWallet(t *testing.T) {
	contexts := []http.Context{
		scopeContext{ctx: context.Background()},
		scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyWallet, (*models.Wallet)(nil))},
	}
	for _, ctx := range contexts {
		for _, request := range []preparer{&StoreRequest{}, &EstimateRequest{}} {
			data := chainData{}
			if err := request.PrepareForValidation(ctx, data); err != nil {
				t.Fatalf("%T: %v", request, err)
			}
			if _, ok := data.Get("_chain"); ok {
				t.Fatalf("%T set a chain without a wallet", request)
			}
		}
	}
}

type chainData map[string]any

func (d chainData) Get(key string) (any, bool) {
	value, ok := d[key]
	return value, ok
}

func (d chainData) Set(key string, value any) error {
	d[key] = value
	return nil
}

type scopeContext struct {
	ctx context.Context
}

func (c scopeContext) Deadline() (time.Time, bool)    { return c.ctx.Deadline() }
func (c scopeContext) Done() <-chan struct{}          { return c.ctx.Done() }
func (c scopeContext) Err() error                     { return c.ctx.Err() }
func (c scopeContext) Value(key any) any              { return c.ctx.Value(key) }
func (c scopeContext) Context() context.Context       { return c.ctx }
func (c scopeContext) WithContext(context.Context)    {}
func (c scopeContext) WithValue(any, any)             {}
func (c scopeContext) Request() http.ContextRequest   { return nil }
func (c scopeContext) Response() http.ContextResponse { return nil }
