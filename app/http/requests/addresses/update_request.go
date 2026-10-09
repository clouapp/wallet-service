package addresses

import (
	"github.com/goravel/framework/contracts/http"
)

// UpdateRequest changes the label and/or the external user id of an address.
// A field left out is not changed.
type UpdateRequest struct {
	Label          *string `form:"label"            json:"label"`
	ExternalUserID *string `form:"external_user_id" json:"external_user_id"`
}

func (r *UpdateRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *UpdateRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"label":            "max_len:255",
		"external_user_id": "max_len:255",
	}
}
