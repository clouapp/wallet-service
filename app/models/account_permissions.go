package models

// Account permission names are the S3.4.2 code catalog. Listing a name here
// does not grant it to a role. There is no permissions table.
const (
	AccountPermAccountRead        = "account.read"
	AccountPermAccountWrite       = "account.write"
	AccountPermAccountLifecycle   = "account.lifecycle"
	AccountPermUsersRead          = "users.read"
	AccountPermUsersWrite         = "users.write"
	AccountPermRolesRead          = "roles.read"
	AccountPermRolesWrite         = "roles.write"
	AccountPermTokensRead         = "tokens.read"
	AccountPermTokensWrite        = "tokens.write"
	AccountPermSettingsRead       = "settings.read"
	AccountPermSettingsWrite      = "settings.write"
	AccountPermActivityRead       = "activity.read"
	AccountPermWalletsRead        = "wallets.read"
	AccountPermWalletsCreate      = "wallets.create"
	AccountPermWalletsWrite       = "wallets.write"
	AccountPermWalletUsersWrite   = "wallet_users.write"
	AccountPermAddressesCreate    = "addresses.create"
	AccountPermWhitelistWrite     = "whitelist.write"
	AccountPermWebhooksRead       = "webhooks.read"
	AccountPermWebhooksWrite      = "webhooks.write"
	AccountPermWithdrawalsCreate  = "withdrawals.create"
	AccountPermWithdrawalsApprove = "withdrawals.approve"
	AccountPermWithdrawalsCancel  = "withdrawals.cancel"
	AccountPermSweepExecute       = "sweep.execute"
)

// AccountPermissions is that catalog, in the order the plan names it.
func AccountPermissions() []string {
	return []string{
		AccountPermAccountRead,
		AccountPermAccountWrite,
		AccountPermAccountLifecycle,
		AccountPermUsersRead,
		AccountPermUsersWrite,
		AccountPermRolesRead,
		AccountPermRolesWrite,
		AccountPermTokensRead,
		AccountPermTokensWrite,
		AccountPermSettingsRead,
		AccountPermSettingsWrite,
		AccountPermActivityRead,
		AccountPermWalletsRead,
		AccountPermWalletsCreate,
		AccountPermWalletsWrite,
		AccountPermWalletUsersWrite,
		AccountPermAddressesCreate,
		AccountPermWhitelistWrite,
		AccountPermWebhooksRead,
		AccountPermWebhooksWrite,
		AccountPermWithdrawalsCreate,
		AccountPermWithdrawalsApprove,
		AccountPermWithdrawalsCancel,
		AccountPermSweepExecute,
	}
}

// IsAccountPermission reports whether name is one entry in AccountPermissions.
func IsAccountPermission(name string) bool {
	if name == "" {
		return false
	}
	for _, permission := range AccountPermissions() {
		if permission == name {
			return true
		}
	}
	return false
}
