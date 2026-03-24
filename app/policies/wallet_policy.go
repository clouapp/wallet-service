package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
)

// WalletPolicy defines gate abilities for Wallet resources.
// Abilities: wallet.view, wallet.update, wallet.freeze,
//
//	wallet.add-user, wallet.remove-user, wallet.whitelist
type WalletPolicy struct{}

// walletUserRole fetches the caller's role in the given wallet.
func walletUserRole(ctx context.Context, walletID uuid.UUID) string {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return ""
	}
	var wu models.WalletUser
	err := facades.Orm().Query().
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
		First(&wu)
	if err != nil {
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
	var w models.Wallet
	if err := facades.Orm().Query().Where("id = ?", walletID).First(&w); err != nil {
		return ""
	}
	if w.AccountID == nil {
		return ""
	}
	var au models.AccountUser
	if err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", *w.AccountID, userID).
		First(&au); err != nil {
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
