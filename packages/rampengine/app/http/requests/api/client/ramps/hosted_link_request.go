package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// HostedLinkRequest validates POST /api/kyc/{provider}/hosted-link.
//
// The name comes from the caller because no local record holds one: the user
// row has a handle and an email, and the identity name only ever existed
// inside an application payload, which is deliberately not persisted.
type HostedLinkRequest struct {
	requests.Open
	Provider     string   `form:"provider" json:"provider"`
	FullName     string   `form:"full_name" json:"full_name"`
	RedirectURI  string   `form:"redirect_uri" json:"redirect_uri"`
	Endorsements []string `form:"endorsements" json:"endorsements"`
}

func (r *HostedLinkRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"provider":     ProviderRule,
		"full_name":    "required|max_len:128",
		"redirect_uri": "max_len:2048",
	}
}
