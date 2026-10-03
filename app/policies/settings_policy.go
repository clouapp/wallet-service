package policies

// Account settings permissions. The dashboard route pair is settings.view
// and settings.update (the names the settings registry and the front share).
// A group may later demand an extra permission; none of the account groups
// in this pass do.
const (
	PermSettingsView   = "settings.view"
	PermSettingsUpdate = "settings.update"
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
