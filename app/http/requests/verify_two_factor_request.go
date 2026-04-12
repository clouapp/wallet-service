package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type VerifyTwoFactorRequest struct {
	PartialToken string `form:"partial_token" json:"partial_token"`
	Code         string `form:"code"          json:"code"`
	RecoveryCode string `form:"recovery_code" json:"recovery_code"`
}

func (r *VerifyTwoFactorRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *VerifyTwoFactorRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"partial_token": "required",
	}
}
