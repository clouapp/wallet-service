package policies

// Account settings permissions. The dashboard route pair is settings.view
// and settings.update (the names the settings registry and the front share).
// A group that stores a secret declares its own pair on top of this one.
const (
	PermSettingsView   = "settings.view"
	PermSettingsUpdate = "settings.update"
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

const (
	roleOwner   = "owner"
	roleAdmin   = "admin"
	roleAuditor = "auditor"
	roleUser    = "user"
)

// MayViewSettings reports whether the account role holds settings.view.
//
// The plan's read set for account settings is owner, admin and auditor.
// Auditor is read-only on every mutating ability. The user role operates
// wallets and does not administer the account, so it holds neither
// settings.view nor settings.update.
func MayViewSettings(role string) bool {
	switch role {
	case roleOwner, roleAdmin, roleAuditor:
		return true
	default:
		return false
	}
}

// MayUpdateSettings reports whether the account role holds settings.update.
// Owner and admin may write account-managed groups. Auditor and user may not.
func MayUpdateSettings(role string) bool {
	return role == roleOwner || role == roleAdmin
}
