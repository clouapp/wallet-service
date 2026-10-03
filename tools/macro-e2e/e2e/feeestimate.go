package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

const feeEstimatePathFormat = "/api/v1/wallets/%s/fee-estimate?%s"

// DecimalAmountPattern is the human amount POST /withdrawals takes (e.g. 0.00003).
var DecimalAmountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,40})(\.[0-9]{1,36})?$`)

// FeeEstimateRequest prices a withdrawal; Amount is optional (the API then uses the smallest transfer).
type FeeEstimateRequest struct {
	WalletID, Asset, To, Amount string
}

// FeeEstimateResult is the HTTP status and decoded body of GET /fee-estimate.
type FeeEstimateResult struct {
	Status int
	Body   any
}

// FeeEstimate calls the read-only fee-estimate endpoint with the Markets token (kept in memory).
func FeeEstimate(ctx context.Context, request FeeEstimateRequest, token func(context.Context) (string, error), api APIClient) (FeeEstimateResult, error) {
	checks := []fieldCheck{{UUIDPattern, request.WalletID, "wallet id"}, {AssetPattern, request.Asset, "asset"}, {AddressPattern, request.To, "destination address"}}
	if request.Amount != "" {
		checks = append(checks, fieldCheck{DecimalAmountPattern, request.Amount, "decimal amount"})
	}
	if err := requireAll(checks); err != nil {
		return FeeEstimateResult{}, err
	}
	if token == nil {
		return FeeEstimateResult{}, fmt.Errorf("no Markets token source configured")
	}
	bearer, err := token(ctx)
	if err != nil {
		return FeeEstimateResult{}, err
	}
	query := url.Values{"asset": {request.Asset}, "to": {request.To}}
	if request.Amount != "" {
		query.Set("amount", request.Amount)
	}
	status, body, err := api.Request(ctx, http.MethodGet, fmt.Sprintf(feeEstimatePathFormat, request.WalletID, query.Encode()), bearer, nil)
	if err != nil {
		return FeeEstimateResult{}, err
	}
	return FeeEstimateResult{Status: status, Body: body}, nil
}
