package middleware

import (
	"errors"

	contractshttp "github.com/goravel/framework/contracts/http"

	rampsresources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	customerssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/customers"
)

// RefuseClosedCustomer refuses a route that moves money or KYC data for a
// closed customer with 409 customer_closed (API contract §1). It is chained
// after CustomerScope on those routes only: reads, a refresh or a cancel of a
// closed customer keep working, and the app, told the customer is closed
// rather than gone, keeps the company's wallet and history instead of
// falling back to the person's.
func RefuseClosedCustomer() contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		customer := CurrentCustomer(ctx)
		if customer == nil {
			responses.InternalError(ctx, errors.New("RefuseClosedCustomer chained without CustomerScope")).Abort()
			return
		}
		if customer.Status == models.CustomerStatusClosed {
			responses.Error(ctx, contractshttp.StatusConflict, rampsresources.ErrorCode(customerssvc.ErrCustomerClosed)).Abort()
			return
		}
		ctx.Request().Next()
	}
}
