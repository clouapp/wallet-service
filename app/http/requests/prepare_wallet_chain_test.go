package requests

import (
	"context"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/models"
)

func TestPrepareForValidationReadsTheWalletChainFromTheRequest(t *testing.T) {
	wallet := &models.Wallet{Chain: "eth"}
	ctx := scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyWallet, wallet)}
	preparers := []validationPreparer{
		&EstimateWithdrawalRequest{},
		&AddWhitelistEntryRequest{},
		&CreateWalletWithdrawalRequest{},
	}
	for _, preparer := range preparers {
		data := chainData{}
		if err := preparer.PrepareForValidation(ctx, data); err != nil {
			t.Fatalf("%T: %v", preparer, err)
		}
		got, ok := data.Get("_chain")
		if !ok || got != "eth" {
			t.Fatalf("%T chain = %v, %v", preparer, got, ok)
		}
	}
}

func TestPrepareForValidationLeavesTheChainUnsetWhenTheWalletIsMissing(t *testing.T) {
	preparers := []validationPreparer{
		&EstimateWithdrawalRequest{},
		&AddWhitelistEntryRequest{},
		&CreateWalletWithdrawalRequest{},
	}
	contexts := []http.Context{
		scopeContext{ctx: context.Background()},
		scopeContext{ctx: context.WithValue(context.Background(), requestctx.KeyWallet, (*models.Wallet)(nil))},
	}
	for _, ctx := range contexts {
		for _, preparer := range preparers {
			data := chainData{}
			if err := preparer.PrepareForValidation(ctx, data); err != nil {
				t.Fatalf("%T: %v", preparer, err)
			}
			if _, ok := data.Get("_chain"); ok {
				t.Fatalf("%T set a chain without a wallet", preparer)
			}
		}
	}
}

type validationPreparer interface {
	PrepareForValidation(ctx http.Context, data validation.Data) error
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
