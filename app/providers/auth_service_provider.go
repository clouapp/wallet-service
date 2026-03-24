package providers

import (
	"context"

	"github.com/google/uuid"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/policies"
)

// AuthServiceProvider registers Gate abilities for Account and Wallet resources.
type AuthServiceProvider struct{}

func (r *AuthServiceProvider) Register(app foundation.Application) {}

func (r *AuthServiceProvider) Boot(app foundation.Application) {
	gate := facades.Gate()
	if gate == nil {
		return
	}

	ap := &policies.AccountPolicy{}
	wp := &policies.WalletPolicy{}

	toUUID := func(arguments map[string]any, key string) (uuid.UUID, bool) {
		v, ok := arguments[key]
		if !ok {
			return uuid.UUID{}, false
		}
		id, ok := v.(uuid.UUID)
		return id, ok
	}

	gate.Define("account.view", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.View(ctx, arguments)
	})
	gate.Define("account.update", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Update(ctx, arguments)
	})
	gate.Define("account.delete", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Delete(ctx, arguments)
	})
	gate.Define("account.add-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.AddUser(ctx, arguments)
	})
	gate.Define("account.remove-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.RemoveUser(ctx, arguments)
	})
	gate.Define("account.freeze", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Freeze(ctx, arguments)
	})
	gate.Define("account.archive", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Archive(ctx, arguments)
	})

	gate.Define("wallet.view", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.View(ctx, arguments)
	})
	gate.Define("wallet.update", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Update(ctx, arguments)
	})
	gate.Define("wallet.freeze", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Freeze(ctx, arguments)
	})
	gate.Define("wallet.add-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.AddUser(ctx, arguments)
	})
	gate.Define("wallet.remove-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.RemoveUser(ctx, arguments)
	})
	gate.Define("wallet.whitelist", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Whitelist(ctx, arguments)
	})

	_ = toUUID // helper available for future extensions
}
