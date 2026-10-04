package policies

import "github.com/macrowallets/waas/app/models"

// PermActivityRead is the account activity list permission.
const PermActivityRead = models.AccountPermActivityRead

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
