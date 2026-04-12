package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type RefreshTokenRequest struct {
	RefreshToken string `form:"refresh_token" json:"refresh_token"`
}

func (r *RefreshTokenRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *RefreshTokenRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"refresh_token": "required",
	}
}
