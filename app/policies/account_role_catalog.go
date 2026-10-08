package policies

import (
	"sort"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Distinct ids keep the withdrawal-cancel check on the owner gate.
// Equal ids would take the creator shortcut instead.
var (
	catalogActorID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	catalogOtherID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

// AccountRole is one system account role. Rank comes from AccountRoleRank.
// Permissions are the names the live gates already allow. Wallet viewer is
// not an account role.
type AccountRole struct {
	Role        string
	Rank        int
	Permissions []string
}

// AccountRoleCatalog lists owner, admin, auditor and user. Rank is
// owner 3, admin 2, auditor 1, user 1. Each role's permissions are the
// live grant for that role. The owner set also includes a closed-catalog
// name when an existing HTTP gate already allows the owner and the live
// grant omitted it. roles.write is never granted: nothing here writes
// account_role_permissions. This catalog does not change Can or WalletCan.
func AccountRoleCatalog() []AccountRole {
	names := models.AccountRoles()
	catalog := make([]AccountRole, 0, len(names))
	for _, role := range names {
		rank, ok := AccountRoleRank(role)
		if !ok {
			panic("account role catalog: " + role + " has no rank")
		}
		catalog = append(catalog, AccountRole{
			Role:        role,
			Rank:        rank,
			Permissions: catalogPermissions(role),
		})
	}
	return catalog
}

func catalogPermissions(role string) []string {
	held := map[string]struct{}{}
	for _, permission := range rolePermissions(role) {
		rememberCatalogPermission(held, permission)
	}
	if role == roleOwner {
		for _, permission := range ownerPermissionsAlreadyAllowed() {
			rememberCatalogPermission(held, permission)
		}
	}
	names := make([]string, 0, len(held))
	for permission := range held {
		names = append(names, permission)
	}
	sort.Strings(names)
	return names
}

func rememberCatalogPermission(held map[string]struct{}, permission string) {
	if permission == "" || permission == models.AccountPermRolesWrite || !models.IsAccountPermission(permission) {
		return
	}
	held[permission] = struct{}{}
}

// ownerPermissionsAlreadyAllowed is the closed-catalog names the live owner
// grant does not list, and that an existing handler already allows an owner.
// withdrawals.approve has no handler. roles.write is refused by the caller.
func ownerPermissionsAlreadyAllowed() []string {
	membership := WalletMembership{AccountRole: roleOwner, UserID: catalogActorID}
	allowed := make([]string, 0, 8)
	if memberMayReadAccount(roleOwner) {
		allowed = append(allowed, models.AccountPermAccountRead)
	}
	if mayWriteAccount(roleOwner) {
		allowed = append(allowed, models.AccountPermAccountWrite)
	}
	if mayChangeAccountLifecycle(roleOwner) {
		allowed = append(allowed, models.AccountPermAccountLifecycle)
	}
	if WalletUpdate(membership).Allowed() || WalletFreeze(membership).Allowed() {
		allowed = append(allowed, models.AccountPermWalletsWrite)
	}
	if WalletAddUser(membership).Allowed() {
		allowed = append(allowed, models.AccountPermWalletUsersWrite)
	}
	if WalletWhitelist(membership).Allowed() {
		allowed = append(allowed, models.AccountPermWhitelistWrite)
	}
	if ownerMayReadWebhooks() {
		allowed = append(allowed, models.AccountPermWebhooksRead)
	}
	if WalletCancelWithdrawal(membership, catalogOtherID).Allowed() {
		allowed = append(allowed, models.AccountPermWithdrawalsCancel)
	}
	return allowed
}

// memberMayReadAccount reports whether GET /v1/accounts/{accountId} admits role.
// AccountContext admits every active member. The four system roles qualify.
func memberMayReadAccount(role string) bool {
	return KnownAccountRole(role)
}

// ownerMayReadWebhooks reports whether the owner may GET a wallet's webhooks.
// The list handler adds no role check after WalletContext, and the owner may
// open every account wallet. The token holding already names webhooks.read
// for the owner.
func ownerMayReadWebhooks() bool {
	return SeesEveryAccountWallet(roleOwner, false) &&
		HoldsAPITokenPermission(roleOwner, models.AccountPermWebhooksRead)
}
