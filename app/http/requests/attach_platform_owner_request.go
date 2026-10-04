package requests

import "github.com/goravel/framework/contracts/http"

// AttachPlatformOwnerRequest is the body of POST /v1/platform/accounts/{accountId}/owners.
// Account member add names email, so this recovery path names email and not user_id.
type AttachPlatformOwnerRequest struct {
	Email string `form:"email" json:"email"`
}

func (r *AttachPlatformOwnerRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *AttachPlatformOwnerRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "trim",
	}
}

func (r *AttachPlatformOwnerRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "required|email",
	}
}
