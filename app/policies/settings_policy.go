package policies

// Platform settings permissions. The platform route pair is settings.view
// and settings.update. The account dashboard uses settings.read and
// settings.write. A group that stores a secret declares its own pair on
// top of the route pair.
const (
	PermSettingsView   = "settings.view"
	PermSettingsUpdate = "settings.update"
)

// PermSettingsRead and PermSettingsWrite are the account guard S1.4.7
// names on the dashboard settings routes. Owner, admin, and auditor hold
// settings.read. Owner and admin hold settings.write. Auditor does not
// write. The user role holds neither. settings.security.write on
// account_security is optional and is not required, so it is not declared
// and it is not a gate. MayViewSettings and MayUpdateSettings stay the
// role check, so today's readers and writers stay authorized. Platform
// names stay settings.view and settings.update.
const (
	PermSettingsRead  = "settings.read"
	PermSettingsWrite = "settings.write"
)

// PermMailView and PermMailUpdate are the extra pair on a platform mail
// group that stores a live credential (S1.4.7). Holding settings.update
// does not grant that credential. No account role holds either name:
// owner, admin, auditor, and user may neither read nor write it. There is
// no platform permission catalog, so a platform_admins row stands in.
const (
	PermMailView   = "mail.view"
	PermMailUpdate = "mail.update"
)

// PermProvidersView and PermProvidersUpdate are the extra pair on a
// platform group that stores a chain-data or price provider key (S1.4.7).
// Holding settings.update does not grant that key. No account role holds
// either name: owner, admin, auditor, and user may neither read nor write
// it. There is no platform permission catalog, so a platform_admins row
// stands in.
const (
	PermProvidersView   = "providers.view"
	PermProvidersUpdate = "providers.update"
)

// PermSweepView and PermSweepUpdate are the pair S1.4.7 names on
// sweep_limits, account_sweep_limits, and the chain-row sweep threshold
// columns. Holding settings.update does not grant that pair. No account
// role holds either name. Owner, admin, and auditor still read
// account_sweep_limits through settings.read. There is no platform
// permission catalog, so a platform_admins row stands in for the write.
const (
	PermSweepView   = "sweep.view"
	PermSweepUpdate = "sweep.update"
)

// PermChainsView and PermChainsUpdate are the pair S1.4.7 names on
// PATCH /v1/platform/chains/{chainId} and PATCH /v1/platform/chains/{chainId}/rpc.
// Holding settings.update does not grant that pair. No account role holds
// either name. The RPC URL stays write-only and is never returned. There is
// no platform permission catalog, so a platform_admins row stands in. The
// pair is not a second gate on those routes.
const (
	PermChainsView   = "chains.view"
	PermChainsUpdate = "chains.update"
)

const (
	roleOwner   = "owner"
	roleAdmin   = "admin"
	roleAuditor = "auditor"
	roleUser    = "user"
)

// MayViewSettings reports whether the account role holds settings.read.
//
// The plan's read set for account settings is owner, admin and auditor.
// Auditor is read-only on every mutating ability. The user role operates
// wallets and does not administer the account, so it holds neither
// settings.read nor settings.write. The retired viewer label is not this
// set: it stays refused here so today's 403 remains.
func MayViewSettings(role string) bool {
	switch role {
	case roleOwner, roleAdmin, roleAuditor:
		return Can(AccountRoleGrants(role), PermSettingsRead)
	default:
		return false
	}
}

// MayUpdateSettings reports whether the account role holds settings.write.
// Owner and admin may write account-managed groups. Auditor and user may not.
func MayUpdateSettings(role string) bool {
	switch role {
	case roleOwner, roleAdmin:
		return Can(AccountRoleGrants(role), PermSettingsWrite)
	default:
		return false
	}
}
