package requests

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"
)

type UpdatePreferencesRequest struct {
	PreferredFiatCode string `form:"preferred_fiat_code" json:"preferred_fiat_code"`
	DisplayInFiat     *bool  `form:"display_in_fiat" json:"display_in_fiat"`
}

func (r *UpdatePreferencesRequest) Authorize(_ http.Context) error {
	return nil
}

func (r *UpdatePreferencesRequest) Rules(_ http.Context) map[string]string {
	return map[string]string{
		"preferred_fiat_code": "max_len:20",
	}
}

func (r *UpdatePreferencesRequest) Messages(_ http.Context) map[string]string {
	return map[string]string{}
}

func (r *UpdatePreferencesRequest) Attributes(_ http.Context) map[string]string {
	return map[string]string{}
}

func (r *UpdatePreferencesRequest) PrepareForValidation(_ http.Context, data validation.Data) error {
	return nil
}
