package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

// WalletPolicy defines gate abilities for Wallet resources.
// Abilities: wallet.view, wallet.update, wallet.archive, wallet.freeze,
//
//	wallet.add-user, wallet.remove-user, wallet.whitelist, wallet.manage-webhooks, wallet.cancel-withdrawal
type WalletPolicy struct{}

// WalletMembership is the caller's place on one wallet. The caller loads the
// wallet role and the account role; the policy only decides.
type WalletMembership struct {
	WalletRole  string
	AccountRole string
	UserID      uuid.UUID
}

func (p *WalletPolicy) View(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletView(membershipFrom(arguments))
}

func (p *WalletPolicy) Update(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletUpdate(membershipFrom(arguments))
}

// Archive allows the same wallet or account owner/admin as Update.
// S3 (#12, unmerged) will replace this helper with a per-route permission table.
func (p *WalletPolicy) Archive(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if p.Update(ctx, arguments).Allowed() {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may archive wallets")
}

func (p *WalletPolicy) Freeze(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletFreeze(membershipFrom(arguments))
}

func (p *WalletPolicy) AddUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletAddUser(membershipFrom(arguments))
}

func (p *WalletPolicy) RemoveUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	return p.AddUser(ctx, arguments)
}

func (p *WalletPolicy) Whitelist(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletWhitelist(membershipFrom(arguments))
}

func (p *WalletPolicy) ManageWebhooks(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletManageWebhooks(membershipFrom(arguments))
}

func (p *WalletPolicy) CancelWithdrawal(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments["wallet_id"].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	membership := membershipFrom(arguments)
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	creatorID, ok := arguments["creator_id"].(uuid.UUID)
	if ok && creatorID == membership.UserID {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
}

// WalletView is the wallet.view decision for a membership the caller loaded.
func WalletView(membership WalletMembership) contractsaccess.Response {
	if membership.WalletRole != "" || membership.AccountRole != "" {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("not a member of this wallet or its account")
}

// WalletArchive is the wallet.archive decision. The allow rule matches update;
// the denial names archive so the route can say what was refused.
func WalletArchive(membership WalletMembership) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may archive wallets")
}

// WalletUpdate is the wallet.update decision for a membership the caller loaded.
func WalletUpdate(membership WalletMembership) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may update wallet settings")
}

// WalletFreeze is the wallet.freeze decision for a membership the caller loaded.
func WalletFreeze(membership WalletMembership) contractsaccess.Response {
	if membership.WalletRole == roleOwner || membership.AccountRole == roleOwner || membership.AccountRole == roleAdmin {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only owners and account admins may freeze wallets")
}

// WalletAddUser is the wallet.add-user decision for a membership the caller loaded.
func WalletAddUser(membership WalletMembership) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may add wallet users")
}

// WalletRemoveUser is the wallet.remove-user decision for a membership the caller loaded.
func WalletRemoveUser(membership WalletMembership) contractsaccess.Response {
	return WalletAddUser(membership)
}

// WalletWhitelist is the wallet.whitelist decision for a membership the caller loaded.
func WalletWhitelist(membership WalletMembership) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may manage the whitelist")
}

// WalletManageWebhooks is the wallet.manage-webhooks decision for a membership the caller loaded.
func WalletManageWebhooks(membership WalletMembership) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only wallet/account owners and admins may manage webhooks")
}

// WalletCancelWithdrawal is the wallet.cancel-withdrawal decision.
// The creator may cancel their own withdrawal; owners and admins may cancel any.
func WalletCancelWithdrawal(membership WalletMembership, creatorID uuid.UUID) contractsaccess.Response {
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	if creatorID == membership.UserID {
		return access.NewAllowResponse()
	}
	return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
}

func mayAdministerWallet(membership WalletMembership) bool {
	return membership.WalletRole == roleOwner || membership.WalletRole == roleAdmin ||
		membership.AccountRole == roleOwner || membership.AccountRole == roleAdmin
}

func membershipFrom(arguments map[string]any) WalletMembership {
	if arguments == nil {
		return WalletMembership{}
	}
	walletRole, _ := arguments["wallet_role"].(string)
	accountRole, _ := arguments["account_role"].(string)
	userID, _ := arguments["user_id"].(uuid.UUID)
	return WalletMembership{
		WalletRole:  walletRole,
		AccountRole: accountRole,
		UserID:      userID,
	}
}
