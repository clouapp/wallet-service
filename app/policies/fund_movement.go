package policies

import "github.com/macrowallets/waas/app/models"

const (
	FundWithdraw        = "withdraw"
	FundSweep           = "sweep"
	FundCreateWallet    = "create_wallet"
	FundGenerateAddress = "generate_address"
)

// MayPerformFundAction reports whether an account role may perform a
// fund-moving action. Owner and admin may withdraw, sweep and create a
// wallet. Owner, admin and user may generate an address. Auditor, and the
// retired viewer label, are read-only. An unknown role is refused.
func MayPerformFundAction(role, action string) bool {
	switch role {
	case models.RetiredAccountRoleViewer:
		role = models.AccountRoleAuditor
	}
	if !models.IsAccountRole(role) {
		return false
	}
	switch action {
	case FundWithdraw, FundSweep, FundCreateWallet:
		return role == models.AccountRoleOwner || role == models.AccountRoleAdmin
	case FundGenerateAddress:
		return role == models.AccountRoleOwner || role == models.AccountRoleAdmin || role == models.AccountRoleUser
	default:
		return false
	}
}
