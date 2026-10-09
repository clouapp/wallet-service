package auth

import (
	"github.com/goravel/framework/contracts/http"
)

type VerifyTwoFactorRequest struct {
	ChallengeToken string `form:"challenge_token" json:"challenge_token"`
	Code           string `form:"code"            json:"code"`
	RecoveryCode   string `form:"recovery_code"   json:"recovery_code"`
}

func (r *VerifyTwoFactorRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *VerifyTwoFactorRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"challenge_token": "required",
	}
}
