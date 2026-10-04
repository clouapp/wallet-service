package policies

import (
	"encoding/json"
	"strings"

	"github.com/goravel/framework/auth/access"
	contractsaccess "github.com/goravel/framework/contracts/auth/access"
)

// API token permissions are the account-catalog names S3.4.6 allows on a
// minted token. They are not a second vocabulary.
const (
	PermWalletsRead       = "wallets.read"
	PermWalletsCreate     = "wallets.create"
	PermAddressesCreate   = "addresses.create"
	PermWithdrawalsCreate = "withdrawals.create"
	PermSweepExecute      = "sweep.execute"
	PermWebhooksRead      = "webhooks.read"
	PermWebhooksWrite     = "webhooks.write"
	PermTransactionsRead  = "transactions.read"

	apiTokenPermissionNotHeld = "cannot grant a permission you do not hold"
)

// APITokenPermissionCatalog is the closed set a minted token may name.
func APITokenPermissionCatalog() []string {
	return []string{
		PermWalletsRead,
		PermWalletsCreate,
		PermAddressesCreate,
		PermWithdrawalsCreate,
		PermSweepExecute,
		PermWebhooksRead,
		PermWebhooksWrite,
		PermTransactionsRead,
	}
}

// IsAPITokenPermission reports whether name is in the API token catalog.
func IsAPITokenPermission(name string) bool {
	for _, permission := range APITokenPermissionCatalog() {
		if permission == name {
			return true
		}
	}
	return false
}

// HoldsAPITokenPermission reports whether role holds one API token permission.
// Holdings are the S3.4.2 role grants limited to the S3.4.6 catalog:
// owner and admin hold the whole catalog; user holds the operate set named
// for that role; auditor holds every *.read name in the catalog.
func HoldsAPITokenPermission(role, permission string) bool {
	if !IsAPITokenPermission(permission) {
		return false
	}
	switch role {
	case roleOwner, roleAdmin:
		return true
	case roleUser:
		switch permission {
		case PermWalletsRead, PermAddressesCreate, PermWithdrawalsCreate, PermSweepExecute, PermWebhooksRead:
			return true
		default:
			return false
		}
	case roleAuditor:
		switch permission {
		case PermWalletsRead, PermWebhooksRead, PermTransactionsRead:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// APITokenAllows reports whether stored permissions may call a route that
// requires permission. A blank store is the omitted grant, and that token
// keeps the access it has today: S3.4.6 does not take it away. A JSON array
// is limited to the names it lists, including an empty array. Any other
// store denies. An empty permission name, or a name outside the catalog,
// denies: that is a wiring bug, not a grant.
func APITokenAllows(stored, permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" || !IsAPITokenPermission(permission) {
		return false
	}
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return true
	}
	var permissions []string
	if err := json.Unmarshal([]byte(stored), &permissions); err != nil {
		return false
	}
	for _, name := range permissions {
		if name == permission {
			return true
		}
	}
	return false
}

// MintAPITokenPermissions is the MayGrant analogue for a token: every named
// permission must be one the creator's role holds. An empty list grants nothing.
func MintAPITokenPermissions(role string, permissions []string) contractsaccess.Response {
	for _, permission := range permissions {
		if !HoldsAPITokenPermission(role, permission) {
			return access.NewDenyResponse(apiTokenPermissionNotHeld)
		}
	}
	return access.NewAllowResponse()
}
