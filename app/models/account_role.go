package models

// Account roles are one vocabulary for the dashboard and the external API.
// Rank is owner > admin > user = auditor. "viewer" is the retired name for auditor.
const (
	AccountRoleOwner   = "owner"
	AccountRoleAdmin   = "admin"
	AccountRoleUser    = "user"
	AccountRoleAuditor = "auditor"

	// RetiredAccountRoleViewer is accepted only as a migration source.
	RetiredAccountRoleViewer = "viewer"
)

// AccountRoles is the closed set stored in account_users.role.
func AccountRoles() []string {
	return []string{AccountRoleOwner, AccountRoleAdmin, AccountRoleAuditor, AccountRoleUser}
}

// AccountRoleInRule is the Goravel validation rule for that set.
func AccountRoleInRule() string {
	return "required|in:" + AccountRoleOwner + "," + AccountRoleAdmin + "," + AccountRoleAuditor + "," + AccountRoleUser
}

// IsAccountRole reports whether role is one of the four live roles.
func IsAccountRole(role string) bool {
	switch role {
	case AccountRoleOwner, AccountRoleAdmin, AccountRoleAuditor, AccountRoleUser:
		return true
	default:
		return false
	}
}

// AccountRoleRank is the ladder owner > admin > user = auditor.
// Roles outside the ladder rank 0 so they never outrank a real role by accident;
// callers still have to reject them before granting.
func AccountRoleRank(role string) int {
	switch role {
	case AccountRoleOwner:
		return 3
	case AccountRoleAdmin:
		return 2
	case AccountRoleUser, AccountRoleAuditor:
		return 1
	default:
		return 0
	}
}

// AccountRoleOutranks reports whether role carries more authority than actor.
// Equal ranks do not outrank. This is an authorization input and is meant to
// be called from app/policies.
func AccountRoleOutranks(role, actor string) bool {
	return AccountRoleRank(role) > AccountRoleRank(actor)
}
