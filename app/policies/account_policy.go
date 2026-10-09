package policies

import "github.com/macrowallets/waas/app/models"

// Dashboard API-token permissions. Routes stay on
// /v1/accounts/{accountId}/tokens. List is tokens.read; create and revoke
// are tokens.write.
const (
	PermTokensRead  = models.AccountPermTokensRead
	PermTokensWrite = models.AccountPermTokensWrite
)

// PermAccountWrite is PATCH /v1/accounts/{accountId}. Owner and admin hold
// it, the same roles mayWriteAccount already allows. Auditor and user do not.
const PermAccountWrite = models.AccountPermAccountWrite

// PermAccountLifecycle is POST /v1/accounts/{accountId}/freeze and /archive.
// Owner holds it, the same role mayChangeAccountLifecycle already allows.
// Admin, auditor and user do not.
const PermAccountLifecycle = models.AccountPermAccountLifecycle

// Ability argument keys shared by the account and wallet abilities.
const (
	ArgUserID      = "user_id"
	ArgAccountRole = "account_role"
)

// mayWriteAccount reports whether role may PATCH the account.
// Owner and admin may. Auditor and user may not.
func mayWriteAccount(role string) bool {
	return role == roleOwner || role == roleAdmin
}

// mayChangeAccountLifecycle reports whether role may freeze or archive the account.
// Owner may. Admin, auditor and user may not.
func mayChangeAccountLifecycle(role string) bool {
	return role == roleOwner
}

// MayReadTokens reports whether the account role holds tokens.read.
// Owner, admin and auditor may. User may not.
func MayReadTokens(role string) bool {
	switch role {
	case roleOwner, roleAdmin, roleAuditor:
		return true
	default:
		return false
	}
}

// MayWriteTokens reports whether the account role holds tokens.write.
// Owner and admin may. Auditor and user may not.
func MayWriteTokens(role string) bool {
	switch role {
	case roleOwner, roleAdmin:
		return true
	default:
		return false
	}
}
