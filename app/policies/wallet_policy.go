package policies

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

// Wallet gate abilities. The values are the live catalog.
const (
	AbilityWalletView             = "wallet.view"
	AbilityWalletUpdate           = "wallet.update"
	AbilityWalletArchive          = "wallet.archive"
	AbilityWalletFreeze           = "wallet.freeze"
	AbilityWalletAddUser          = "wallet.add-user"
	AbilityWalletRemoveUser       = "wallet.remove-user"
	AbilityWalletWhitelist        = "wallet.whitelist"
	AbilityWalletManageWebhooks   = "wallet.manage-webhooks"
	AbilityWalletCancelWithdrawal = "wallet.cancel-withdrawal"
)

// Wallet gate argument keys. user_id and account_role are ArgUserID and ArgAccountRole.
const (
	ArgWalletID   = "wallet_id"
	ArgWalletRole = "wallet_role"
	ArgCreatorID  = "creator_id"
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
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletView(membershipFor(ctx, arguments))
}

func (p *WalletPolicy) Update(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletUpdate(membershipFor(ctx, arguments))
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
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletFreeze(membershipFor(ctx, arguments))
}

func (p *WalletPolicy) AddUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletAddUser(membershipFor(ctx, arguments))
}

func (p *WalletPolicy) RemoveUser(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	return p.AddUser(ctx, arguments)
}

func (p *WalletPolicy) Whitelist(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletWhitelist(membershipFor(ctx, arguments))
}

func (p *WalletPolicy) ManageWebhooks(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	return WalletManageWebhooks(membershipFor(ctx, arguments))
}

func (p *WalletPolicy) CancelWithdrawal(ctx context.Context, arguments map[string]any) contractsaccess.Response {
	if _, ok := arguments[ArgWalletID].(uuid.UUID); !ok {
		return access.NewDenyResponse("missing wallet_id")
	}
	membership := membershipFor(ctx, arguments)
	if membership.WalletRole == "" && membership.AccountRole == "" {
		return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
	}
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	if membership.AccountRole == roleAuditor {
		return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
	}
	creatorID, ok := arguments[ArgCreatorID].(uuid.UUID)
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
// A non-auditor creator who holds a wallet or account role may cancel their
// own withdrawal. Wallet or account owners and admins may cancel any. An
// account auditor is denied even when they created the withdrawal. A nil or
// empty role set is denied.
func WalletCancelWithdrawal(membership WalletMembership, creatorID uuid.UUID) contractsaccess.Response {
	if membership.WalletRole == "" && membership.AccountRole == "" {
		return access.NewDenyResponse("only the creator or an owner/admin may cancel this withdrawal")
	}
	if mayAdministerWallet(membership) {
		return access.NewAllowResponse()
	}
	if membership.AccountRole != roleAuditor && creatorID == membership.UserID {
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
	walletRole, _ := arguments[ArgWalletRole].(string)
	accountRole, _ := arguments[ArgAccountRole].(string)
	userID, _ := arguments[ArgUserID].(uuid.UUID)
	return WalletMembership{
		WalletRole:  walletRole,
		AccountRole: accountRole,
		UserID:      userID,
	}
}

// membershipFor reads the request grants when the caller did not pass an
// account role. A key that is present, including an empty role, stays as the
// caller loaded it.
func membershipFor(ctx context.Context, arguments map[string]any) WalletMembership {
	membership := membershipFrom(arguments)
	if arguments != nil {
		if _, present := arguments[ArgAccountRole]; present {
			return membership
		}
	}
	load := grantLoad(ctx)
	if load == nil {
		return membership
	}
	role, userID, ok := load.role()
	if !ok || role == "" {
		return membership
	}
	if membership.UserID != uuid.Nil && userID != uuid.Nil && membership.UserID != userID {
		return membership
	}
	membership.AccountRole = role
	if membership.UserID == uuid.Nil {
		membership.UserID = userID
	}
	return membership
}
