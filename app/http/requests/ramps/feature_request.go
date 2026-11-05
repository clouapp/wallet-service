package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
)

type FeatureRequest struct {
	requests.Open
	Enabled *bool `json:"enabled" form:"enabled"`
}

func (*FeatureRequest) Rules(http.Context) map[string]string {
	return map[string]string{"enabled": "bool"}
}
func (r *FeatureRequest) After(http.Context) map[string][]string {
	if r.Enabled == nil {
		return map[string][]string{"enabled": {"required"}}
	}
	return nil
}
