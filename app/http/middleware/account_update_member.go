package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/policies"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

// AccountUpdateMember refuses PATCH /v1/accounts/{accountId}/users/{userId}
// unless the account role already stored by AccountContext holds users.write.
// That is the same grant policies.ManagesMembers allows: owner and admin.
// Auditor and user do not hold it. The member is resolved first: a missing
// member is left to the handler, which answers 404, and only a member that
// exists is 403 when users.write is missing. The Gate's account.update-member
// decides, and refuses with the account service's ErrManageMembers sentence.
// Rank, MayGrant, and MayActOn stay in the account service after this check.
// A denial leaves the membership unchanged.
func AccountUpdateMember(accounts *accountsvc.Service) http.Middleware {
	if accounts == nil {
		panic("account update member: the account service is required")
	}
	return authorize(facades.Gate(), policies.AbilityAccountUpdateMember, accountChildSubject(accounts))
}
