// Ozav verifies an individual from the customer record and the documents
// uploaded against it, and POST /verifications is what stops that, not what
// starts it. Measured 03/09 across all 23 customers of the live organization:
// the 20 with no verification were approved; the 3 with one or two stayed
// pending, their checks never leaving not_started. So the two methods that
// would open one refuse: RequestEndorsements is a client route and
// CreateHostedKycLink an admin button, and either would freeze a user's KYC.

package ozav

import (
	"context"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// verificationSession is the standalone Verification resource, decoded only
// for the webhook: Ozav sends its events for sessions nobody here opened, and
// a delivery we cannot parse is one we cannot dismiss.
type verificationSession struct {
	CustomerID string `json:"customer_id"`
}

// RequestEndorsements refuses: the only way to ask Ozav for a check by name is
// to open a verification, which freezes the customer. Returning the current
// state instead would report success for an ask this provider cannot honour.
func (a *Adapter) RequestEndorsements(context.Context, ramp.CustomerRef, []string) (*ramp.KycState, error) {
	return nil, ramp.ErrVerificationFreezesCustomer
}

// CreateHostedKycLink refuses: the hosted page is the same verification POST
// with a redirect_url, the same freeze, and the one an operator reaches for
// when a user looks stuck. Its live session_url also answered
// ENDPOINT_DEPRECATED (03/09).
func (a *Adapter) CreateHostedKycLink(context.Context, ramp.CustomerRef, ramp.HostedLinkOpts) (*ramp.HostedKycLink, error) {
	return nil, ramp.ErrVerificationFreezesCustomer
}
