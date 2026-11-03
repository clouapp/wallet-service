package middleware

import (
	"context"

	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// BindMacroCustomer is the adapter boundary after Macro has authenticated its
// own actor and checked the UUID customer's tenant. The legacy contact is an
// applicant/representative, never a Macro login or the source of authorization.
func BindMacroCustomer(ctx http.Context, contact *models.User, customer *models.Customer, member *models.CustomerMember) {
	bound := context.WithValue(ctx.Context(), userKey{}, contact)
	bound = context.WithValue(bound, customerKey{}, &scopedCustomer{customer: customer, member: member})
	ctx.WithContext(bound)
}
