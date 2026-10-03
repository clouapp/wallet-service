package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

// WalletPolicy defines gate abilities for Wallet resources.
// Abilities: wallet.view, wallet.update, wallet.freeze,
//
//	wallet.add-user, wallet.remove-user, wallet.whitelist, wallet.manage-webhooks, wallet.cancel-withdrawal
type WalletPolicy struct{}

// walletUserRole fetches the caller's role in the given wallet.
func walletUserRole(ctx context.Context, walletID uuid.UUID) string {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return ""
	}
	wu, err := container.MustMake[*repositories.WalletUserRepository]().FindByWalletAndUser(ctx, walletID, userID)
	if err != nil || wu == nil {
		return ""
	}
	return wu.Roles
}

// accountRoleForWallet fetches the caller's account-level role for the wallet's account.
func accountRoleForWallet(ctx context.Context, walletID uuid.UUID) string {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return ""
	}
	w, err := container.MustMake[*repositories.WalletRepository]().FindByID(ctx, walletID)
	if err != nil || w == nil {
		return ""
	}
	if w.AccountID == nil {
		return ""
	}
	au, err := container.Get().AccountUserRepo.FindByAccountAndUser(ctx, *w.AccountID, userID)
	if err != nil || au == nil {
		return ""
	}
	return au.Role
}

func (p *WalletPolicy) View(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	if walletUserRole(ctx, walletID) != "" || accountRoleForWallet(ctx, walletID) != "" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("not a member of this wallet or its account")
}

func (p *WalletPolicy) Update(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || role == "admin" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may update wallet settings")
}

func (p *WalletPolicy) Freeze(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and account admins may freeze wallets")
}

func (p *WalletPolicy) AddUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || role == "admin" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may add wallet users")
}

func (p *WalletPolicy) RemoveUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	return p.AddUser(ctx, arguments)
}

func (p *WalletPolicy) Whitelist(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || role == "admin" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may manage the whitelist")
}

func (p *WalletPolicy) ManageWebhooks(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || role == "admin" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may manage webhooks")
}

func (p *WalletPolicy) CancelWithdrawal(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	walletID, ok := arguments["wallet_id"].(uuid.UUID)
	if !ok {
		return access.NewDenyResponse("missing wallet_id")
	}

	role := walletUserRole(ctx, walletID)
	accRole := accountRoleForWallet(ctx, walletID)
	if role == "owner" || role == "admin" || accRole == "owner" || accRole == "admin" {
		return access.NewAllowResponse()
	}

	userID, _ := ctx.Value("user_id").(uuid.UUID)
	creatorID, ok := arguments["creator_id"].(uuid.UUID)
	if ok && creatorID == userID {
		return access.NewAllowResponse()
	}

	return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
}

// WalletUpdate is the wallet.update decision for one wallet.
func WalletUpdate(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).Update(ctx, map[string]any{"wallet_id": walletID})
}

// WalletFreeze is the wallet.freeze decision for one wallet.
func WalletFreeze(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).Freeze(ctx, map[string]any{"wallet_id": walletID})
}

// WalletAddUser is the wallet.add-user decision for one wallet.
func WalletAddUser(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).AddUser(ctx, map[string]any{"wallet_id": walletID})
}

// WalletRemoveUser is the wallet.remove-user decision for one wallet.
func WalletRemoveUser(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).RemoveUser(ctx, map[string]any{"wallet_id": walletID})
}

// WalletWhitelist is the wallet.whitelist decision for one wallet.
func WalletWhitelist(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).Whitelist(ctx, map[string]any{"wallet_id": walletID})
}

// WalletManageWebhooks is the wallet.manage-webhooks decision for one wallet.
func WalletManageWebhooks(ctx context.Context, walletID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).ManageWebhooks(ctx, map[string]any{"wallet_id": walletID})
}

// WalletCancelWithdrawal is the wallet.cancel-withdrawal decision.
// The creator may cancel their own withdrawal; owners and admins may cancel any.
func WalletCancelWithdrawal(ctx context.Context, walletID, creatorID uuid.UUID) contractsaccess.Response {
	return (&WalletPolicy{}).CancelWithdrawal(ctx, map[string]any{"wallet_id": walletID, "creator_id": creatorID})
}
