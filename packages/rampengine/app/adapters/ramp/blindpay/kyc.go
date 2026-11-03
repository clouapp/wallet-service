package blindpay

import (
	"context"
	"fmt"
	"net/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// rfiResponse is GET /customers/{id}/rfi. BlindPay answers null when no
// request for information is open, which leaves the pointer nil.
type rfiResponse struct {
	Status  string `json:"status"`
	Request []struct {
		Fields []struct {
			Key string `json:"key"`
		} `json:"fields"`
	} `json:"request"`
}

// StartIntegration has nothing to create: the customer is born atomically
// with its KYC (Task 5). Without one the integration is pending.
func (a *Adapter) StartIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	if ref.ExternalID == "" {
		return &ramp.Integration{Status: models.IntegrationStatusPending}, nil
	}
	return a.RefreshIntegration(ctx, ref)
}

func (a *Adapter) RefreshIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	customer, raw, err := a.fetchCustomer(ctx, client, ref.ExternalID)
	if err != nil {
		return nil, err
	}
	return &ramp.Integration{
		ExternalCustomerID: customer.ID,
		Status:             models.IntegrationStatusActive,
		Raw:                raw,
	}, nil
}

func (a *Adapter) GetKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	return a.readKyc(ctx, ref)
}

func (a *Adapter) RefreshKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	return a.readKyc(ctx, ref)
}

func (a *Adapter) GetRequirements(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycRequirements, error) {
	state, err := a.readKyc(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &ramp.KycRequirements{
		Status:       state.Status,
		Missing:      state.Requirements,
		Endorsements: state.Endorsements,
		Raw:          state.Raw,
	}, nil
}

// RequestEndorsements only re-reads: BlindPay has no endorsement to request,
// bank_payout and pix follow from the customer itself.
func (a *Adapter) RequestEndorsements(ctx context.Context, ref ramp.CustomerRef, endorsements []string) (*ramp.KycState, error) {
	for _, name := range endorsements {
		if name != models.EndorsementBankPayout && name != models.EndorsementPix {
			return nil, fmt.Errorf("blindpay: endorsement %q: %w", name, ramp.ErrNoProviderAvailable)
		}
	}
	return a.readKyc(ctx, ref)
}

// SubmitApplication is not the BlindPay path: the service uses the atomic port.
func (a *Adapter) SubmitApplication(context.Context, ramp.CustomerRef, ramp.Application) (*ramp.KycState, error) {
	return nil, errKycNotUpdatable
}

// SubmitDocuments is not the BlindPay path: the service uses the atomic port.
func (a *Adapter) SubmitDocuments(context.Context, ramp.CustomerRef, ramp.Documents) (*ramp.KycState, error) {
	return nil, errKycNotUpdatable
}

var errKycNotUpdatable = fmt.Errorf("blindpay creates customers atomically; KYC cannot be updated: %w", ramp.ErrDocumentsNotSupported)

func (a *Adapter) readKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	customer, raw, err := a.fetchCustomer(ctx, client, ref.ExternalID)
	if err != nil {
		return nil, err
	}
	state := kycStateFrom(customer, raw)
	keys, err := openRFIKeys(ctx, client, ref.ExternalID)
	if err != nil {
		// The RFI is only what the customer owes, so a failed read of it
		// degrades to the customer alone — unless the customer is waiting on
		// exactly that answer. Then the keys are the read's whole point, and
		// the sync writes Requirements unconditionally: an empty answer would
		// erase the pending steps the last read found, so it fails instead.
		// Client.Do has already logged the failed call as a warning.
		if state.Status == models.KycStatusRequiresInfo {
			return nil, err
		}
		return state, nil
	}
	state.Requirements = append(state.Requirements, keys...)
	return state, nil
}

// openRFIKeys reads the customer's open request for information and answers
// the keys of the fields it asks for; none when no request is open.
func openRFIKeys(ctx context.Context, client *Client, customerID string) ([]string, error) {
	rfi, _, err := ramp.Call[*rfiResponse](ctx, func(ctx context.Context, req Request, out any) error {
		return client.Do(ctx, req, out)
	}, Request{Method: http.MethodGet, Path: "customers/" + customerID + "/rfi"})
	if err != nil {
		return nil, err
	}
	if rfi == nil || rfi.Status != "pending" {
		return nil, nil
	}
	var keys []string
	for _, group := range rfi.Request {
		for _, field := range group.Fields {
			if field.Key != "" {
				keys = append(keys, field.Key)
			}
		}
	}
	return keys, nil
}
