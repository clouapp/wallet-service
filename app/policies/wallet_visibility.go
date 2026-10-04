package policies

import "github.com/macrowallets/waas/app/models"

// SeesEveryAccountWallet reports whether role may list and open every wallet
// of the account. Owner and admin always may. User and auditor may only when
// viewAllWallets is set; otherwise they see the wallets they belong to.
// A stored viewer is the retired name for auditor and follows that rule.
// An unknown role does not see the account-wide list.
func SeesEveryAccountWallet(role string, viewAllWallets bool) bool {
	if role == models.RetiredAccountRoleViewer {
		role = models.AccountRoleAuditor
	}
	switch role {
	case models.AccountRoleOwner, models.AccountRoleAdmin:
		return true
	case models.AccountRoleUser, models.AccountRoleAuditor:
		return viewAllWallets
	default:
		return false
	}
}
