package requests

import "github.com/goravel/framework/contracts/http"

type AcceptInviteRequest struct {
	Token    string `form:"token"     json:"token"`
	Password string `form:"password"  json:"password"`
	FullName string `form:"full_name" json:"full_name"`
}

func (r *AcceptInviteRequest) Authorize(ctx http.Context) error { return nil }

func (r *AcceptInviteRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"token": "required",
	}
}
