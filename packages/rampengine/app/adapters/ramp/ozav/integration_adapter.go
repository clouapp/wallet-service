package ozav

import (
	"context"
	"net/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// StartIntegration makes no call before the customer exists: Ozav needs an
// e-mail to create one, and the customer is born at SubmitApplication.
func (a *Adapter) StartIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	if ref.ExternalID == "" {
		return &ramp.Integration{Status: models.IntegrationStatusPending}, nil
	}
	return a.RefreshIntegration(ctx, ref)
}

// RefreshIntegration confirms the customer still exists upstream. Ozav has no
// offboarded state, so any customer it answers for is an active link.
func (a *Adapter) RefreshIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
	if err != nil {
		return nil, err
	}
	return &ramp.Integration{ExternalCustomerID: resp.ID, Status: models.IntegrationStatusActive, Raw: raw}, nil
}
