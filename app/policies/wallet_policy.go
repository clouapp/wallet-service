package policies

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

// Wallet gate abilities the route guards ask.
const (
	AbilityWalletArchive          = "wallet.archive"
	AbilityWalletUpdate           = "wallet.update"
	AbilityWalletFreeze           = "wallet.freeze"
	AbilityWalletAddUser          = "wallet.add-user"
	AbilityWalletRemoveUser       = "wallet.remove-user"
	AbilityWalletWhitelist        = "wallet.whitelist"
	AbilityWalletManageWebhooks   = "wallet.manage-webhooks"
	AbilityWalletCancelWithdrawal = "wallet.cancel-withdrawal"
)

// Wallet ability argument keys. user_id and account_role are ArgUserID and ArgAccountRole.
const (
	ArgWalletRole = "wallet_role"
	ArgCreatorID  = "creator_id"
)

// WalletMembership is the caller's place on one wallet. The caller loads the
// wallet role and the account role; the policy only decides.
type WalletMembership struct {
	WalletRole  string
	AccountRole string
	UserID      uuid.UUID
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
