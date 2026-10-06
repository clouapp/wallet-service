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

	gate.Define(policies.AbilityAccountView, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.View(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountUpdate, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Update(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountDelete, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Delete(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountAddUser, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.AddUser(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountRemoveUser, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.RemoveUser(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountFreeze, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Freeze(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.AbilityAccountArchive, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.Archive(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.PermTokensRead, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.ReadTokens(ctx, withAccountUser(ctx, arguments))
	})
	gate.Define(policies.PermTokensWrite, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return ap.WriteTokens(ctx, withAccountUser(ctx, arguments))
	})

	gate.Define(policies.AbilityWalletView, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.View(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletUpdate, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Update(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletArchive, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Archive(ctx, arguments)
	})
	gate.Define(policies.AbilityWalletFreeze, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Freeze(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletAddUser, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.AddUser(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletRemoveUser, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.RemoveUser(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletWhitelist, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.Whitelist(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletManageWebhooks, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.ManageWebhooks(ctx, withWalletMembership(ctx, arguments))
	})
	gate.Define(policies.AbilityWalletCancelWithdrawal, func(ctx context.Context, arguments map[string]any) contractsaccess.Response {
		return wp.CancelWithdrawal(ctx, withWalletMembership(ctx, arguments))
	})

	_ = toUUID // helper available for future extensions
}

// withAccountUser copies the gate arguments and attaches the user id and
// account role scope middleware already stored. A value already present is
// left alone. A missing user id leaves that key unset, which the policy
// treats as no membership.
func withAccountUser(ctx context.Context, arguments map[string]any) map[string]any {
	out := make(map[string]any, len(arguments)+2)
	for key, value := range arguments {
		out[key] = value
	}
	if _, present := out[policies.ArgAccountRole]; !present {
		if role, ok := requestctx.AccountRole(ctx); ok {
			out[policies.ArgAccountRole] = role
		}
	}
	if _, ok := out[policies.ArgUserID].(uuid.UUID); ok {
		return out
	}
	userID, ok := requestctx.UserID(ctx)
	if !ok || userID == uuid.Nil {
		return out
	}
	out[policies.ArgUserID] = userID
	return out
}

// withWalletMembership copies the gate arguments and attaches the roles the
// wallet policy used to load itself. A missing user id leaves the roles empty.
func withWalletMembership(ctx context.Context, arguments map[string]any) map[string]any {
	out := make(map[string]any, len(arguments)+3)
	for key, value := range arguments {
		out[key] = value
	}
	walletID, ok := out[policies.ArgWalletID].(uuid.UUID)
	if !ok {
		return out
	}
	userID, userOK := requestctx.UserID(ctx)
	out[policies.ArgUserID] = userID
	if !userOK {
		return out
	}
	walletRole, accountRole := walletrecords.NewMemberships(walletrecords.MembershipsDeps{
		Wallets:  container.MustMake[*walletrecords.Wallets](),
		Members:  container.MustMake[*walletrecords.Members](),
		Accounts: container.MustMake[*accountsvc.Service](),
	}).ForWallet(ctx, walletID, userID)
	out[policies.ArgWalletRole] = walletRole
	out[policies.ArgAccountRole] = accountRole
	return out
}
