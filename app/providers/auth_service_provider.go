package providers

import (
	"context"

	"github.com/google/uuid"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// AuthServiceProvider registers Gate abilities for Account and Wallet resources.
type AuthServiceProvider struct{}

func (r *AuthServiceProvider) Register(app foundation.Application) {}

func (r *AuthServiceProvider) Boot(app foundation.Application) {
	gate := facades.Gate()
	if gate == nil {
		return
	}

	policies.BindAccountUsers(container.MustMake[*repositories.AccountUserRepository]())

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
		return ap.View(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.update", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Update(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.delete", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Delete(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.add-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.AddUser(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.remove-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.RemoveUser(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.freeze", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Freeze(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define("account.archive", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Archive(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.PermTokensRead, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.ReadTokens(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.PermTokensWrite, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.WriteTokens(ctx, withAccountUser(ctx, arguments))
	})

	gate.Define("wallet.view", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.View(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.update", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Update(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.freeze", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Freeze(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.add-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.AddUser(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.remove-user", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.RemoveUser(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.whitelist", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Whitelist(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.manage-webhooks", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.ManageWebhooks(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define("wallet.cancel-withdrawal", func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.CancelWithdrawal(ctx, withWalletMembership(ctx, arguments))
	})

	_ = toUUID // helper available for future extensions
}

// withAccountUser copies the gate arguments and attaches the user id the
// session already stored. An id already present is left alone. A missing id
// leaves the key unset, which the policy treats as no membership.
func withAccountUser(ctx context.Context, arguments map[string]any) map[string]any {
	out := make(map[string]any, len(arguments)+1)
	for key, value := range arguments {
		out[key] = value
	}
	if _, ok := out["user_id"].(uuid.UUID); ok {
		return out
	}
	userID, ok := requestctx.UserID(ctx)
	if !ok || userID == uuid.Nil {
		return out
	}
	out["user_id"] = userID
	return out
}

// withWalletMembership copies the gate arguments and attaches the roles the
// wallet policy used to load itself. A missing user id leaves the roles empty.
func withWalletMembership(ctx context.Context, arguments map[string]any) map[string]any {
	out := make(map[string]any, len(arguments)+3)
	for key, value := range arguments {
		out[key] = value
	}
	walletID, ok := out["wallet_id"].(uuid.UUID)
	if !ok {
		return out
	}
	userID, userOK := requestctx.UserID(ctx)
	out["user_id"] = userID
	if !userOK {
		return out
	}
	walletRole, accountRole := walletrecords.NewMemberships(
		container.MustMake[*walletrecords.Wallets](),
		container.MustMake[*walletrecords.Members](),
		container.MustMake[*accountsvc.Service](),
	).ForWallet(ctx, walletID, userID)
	out["wallet_role"] = walletRole
	out["account_role"] = accountRole
	return out
}
