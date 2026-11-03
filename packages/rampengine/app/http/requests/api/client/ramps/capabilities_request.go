package ramps

import (
	"slices"
	"strings"

	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// CapabilitiesRequest validates GET /api/kyc/capabilities.
type CapabilitiesRequest struct {
	requests.Open
	// FundingFormats is the comma-separated list of what the app can sign:
	// "deposit" (the default) and "prepared". Unknown entries are ignored.
	FundingFormats string `form:"funding_formats" json:"funding_formats"`
}

func (r *CapabilitiesRequest) Rules(contractshttp.Context) map[string]string {
	return map[string]string{"funding_formats": "max_len:64"}
}

// AcceptsPrepared is whether the app declared it can sign a prepared funding.
func (r *CapabilitiesRequest) AcceptsPrepared() bool {
	return slices.Contains(strings.Split(r.FundingFormats, ","), "prepared")
}
