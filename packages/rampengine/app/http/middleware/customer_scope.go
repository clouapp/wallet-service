package middleware

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/container"
	rampsresources "github.com/macrowallets/waas/packages/rampengine/app/http/resources/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/responses"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	customerssvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/customers"
)

// CustomerHeader names the customer a money request acts for (spec §4, §6).
// The app sets it in one place, its auth interceptor, from the wallet that is
// active.
const CustomerHeader = "X-Xip-Customer-Id"

// customerKey is unexported so that CustomerScope is the only code that can
// put a customer in a request context.
type customerKey struct{}

// scopedCustomer is what CustomerScope resolved: the customer, and the
// membership when a header named it.
type scopedCustomer struct {
	customer *models.Customer
	member   *models.CustomerMember
}

// CustomerScope resolves the customer a money request acts for, and hands it
// to the handler through CurrentCustomer and CurrentMember. It is chained
// after Auth.
//
//   - Without the header, the request acts for the user's individual customer,
//     whose id is the user's, and nothing is read: every request of an app that
//     predates the header, the iOS one included, stays exactly what it was.
//   - With it, the user's membership of the customer it names is loaded. A
//     customer the user is not a member of, or a header that names no customer
//     at all, answers 404 customer_not_found, the same for both so a header
//     cannot probe which ids exist; the app drops the customer id it held. A
//     failed lookup answers 500, never 403 or 404 (http-error-contract.md).
//
// The step-up MFA stays the user's, never the customer's.
func CustomerScope() contractshttp.Middleware {
	return customerScopeWith(container.MustMake[*customerssvc.Service](container.CustomerServiceKey))
}

func customerScopeWith(customers *customerssvc.Service) contractshttp.Middleware {
	return func(ctx contractshttp.Context) {
		user := CurrentUser(ctx)
		if user == nil {
			responses.Error(ctx, contractshttp.StatusUnauthorized, "unauthenticated").Abort()
			return
		}
		scope, err := resolveCustomer(customers, user, ctx.Request().Header(CustomerHeader, ""))
		if errors.Is(err, customerssvc.ErrCustomerNotFound) {
			responses.Error(ctx, contractshttp.StatusNotFound, rampsresources.ErrorCode(err)).Abort()
			return
		}
		if err != nil {
			responses.InternalError(ctx, fmt.Errorf("resolving the customer of user %d: %w", user.ID, err)).Abort()
			return
		}
		ctx.WithContext(context.WithValue(ctx.Context(), customerKey{}, scope))
		ctx.Request().Next()
	}
}

// resolveCustomer is the customer header's customer for user, or the user's
// individual customer when the header is absent.
func resolveCustomer(customers *customerssvc.Service, user *models.User, header string) (*scopedCustomer, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return &scopedCustomer{customer: individualCustomerOf(user)}, nil
	}
	id, err := strconv.ParseUint(header, 10, 64)
	if err != nil || id == 0 {
		return nil, customerssvc.ErrCustomerNotFound
	}
	member, err := customers.Membership(user, uint(id))
	if err != nil {
		return nil, err
	}
	return &scopedCustomer{customer: member.Customer, member: member}, nil
}

// individualCustomerOf is the user's individual customer as every account
// holds it: its id is the user's, it is born active, and a deleted account —
// the only kind whose customer could be anything else — never gets past Auth.
func individualCustomerOf(user *models.User) *models.Customer {
	return &models.Customer{
		Model:  orm.Model{ID: user.ID},
		Type:   models.CustomerTypeIndividual,
		Status: models.CustomerStatusActive,
	}
}

// CurrentCustomer is the customer CustomerScope resolved for the request. It
// is nil only on a route CustomerScope does not guard.
func CurrentCustomer(ctx contractshttp.Context) *models.Customer {
	scope, ok := ctx.Context().Value(customerKey{}).(*scopedCustomer)
	if !ok {
		return nil
	}
	return scope.customer
}

// CurrentMember is the membership the customer header resolved. It is nil
// when the request named no customer: the individual customer is implied by
// the user, who is always its owner, and reading the membership is the query
// the header-less path promises not to make.
func CurrentMember(ctx contractshttp.Context) *models.CustomerMember {
	scope, ok := ctx.Context().Value(customerKey{}).(*scopedCustomer)
	if !ok {
		return nil
	}
	return scope.member
}
