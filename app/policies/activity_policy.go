package policies

// PermActivityRead is the account activity list permission.
const PermActivityRead = "activity.read"

// MayReadActivity reports whether the account role holds activity.read.
// Owner, admin and auditor may list. User may not. Auditor stays read-only:
// this permission does not grant a write.
func MayReadActivity(role string) bool {
	switch role {
	case roleOwner, roleAdmin, roleAuditor:
		return true
	default:
		return false
	}
}
