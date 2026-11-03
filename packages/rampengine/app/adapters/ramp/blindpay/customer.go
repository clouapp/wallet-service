package blindpay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// customerLimit is the customer's payout limit in USD. Pointers, because a
// field BlindPay omits is not a limit of zero.
type customerLimit struct {
	PerTransaction *float64 `json:"per_transaction"`
	Daily          *float64 `json:"daily"`
	Monthly        *float64 `json:"monthly"`
}

type kycWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// customerResponse is the part of GET /customers/{id} the KYC state reads.
// The rest of the body (address, documents, ...) is PII the adapter never
// decodes; it travels only as Raw, for the admin. Country is read for the Pix
// endorsement alone (pixEndorsement).
type customerResponse struct {
	ID          string        `json:"id"`
	CustomerID  string        `json:"customer_id"`
	Country     string        `json:"country"`
	KycType     string        `json:"kyc_type"`
	KycStatus   string        `json:"kyc_status"`
	Email       string        `json:"email"`
	TosID       string        `json:"tos_id"`
	Limit       customerLimit `json:"limit"`
	KycWarnings []kycWarning  `json:"kyc_warnings"`
}

func (a *Adapter) fetchCustomer(ctx context.Context, client *Client, customerID string) (customerResponse, json.RawMessage, error) {
	if customerID == "" {
		return customerResponse{}, nil, ramp.ErrIntegrationRequired
	}
	customer, raw, err := ramp.Call[customerResponse](ctx, func(ctx context.Context, req Request, out any) error {
		return client.Do(ctx, req, out)
	}, Request{Method: http.MethodGet, Path: "customers/" + customerID})
	if err != nil {
		return customerResponse{}, nil, err
	}
	if customer.ID != customerID {
		return customerResponse{}, nil, fmt.Errorf("blindpay: customer answer names another id: %w", ramp.ErrProviderFailed)
	}
	return customer, raw, nil
}
