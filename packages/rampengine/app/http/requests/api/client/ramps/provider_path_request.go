// Package ramps validates the client-facing integration, KYC and ramp
// endpoints.
package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// ProviderRule bounds the {provider} path segment to providers the build knows.
//
// It is a package constant rather than an inlined string in each Rules() —
// the repo rule against sharing validation constants exists so unrelated
// endpoints do not drift into a common definition, and this is the opposite
// case: the set of valid providers is a single fact, and eight copies of it
// would be eight places to forget when one is added.
const ProviderRule = "required|in:bridge,mutualpay,ozav,blindpay"

// ProviderPathRequest validates the endpoints whose only input is the provider
// in the path.
type ProviderPathRequest struct {
	requests.Open
	Provider string `form:"provider" json:"provider"`
}

func (r *ProviderPathRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{"provider": ProviderRule}
}
