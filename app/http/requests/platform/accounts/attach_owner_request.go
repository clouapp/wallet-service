package accounts

import "github.com/goravel/framework/contracts/http"

// AttachOwnerRequest is the body of POST /platform/accounts/{accountId}/owners.
// Account member add names email, so this recovery path names email and not user_id.
type AttachOwnerRequest struct {
	Email string `form:"email" json:"email" example:"owner@example.com"`
}

func (r *AttachOwnerRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *AttachOwnerRequest) Filters(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "trim",
	}
}

func (r *AttachOwnerRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"email": "required|email",
	}
}
