package users

import (
	accountresources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
)

// The types below document request and response bodies in the swagger
// annotations of this package. Handlers do not use them.

type ChangePasswordSwagger struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type UpdateMeSwagger struct {
	FullName string `json:"full_name" example:"Alice Smith"`
}

type UpdateDefaultAccountSwagger struct {
	AccountID string `json:"account_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

type AccountListResponse struct {
	Data   []accountresources.MemberAccount `json:"data"`
	Total  int64                            `json:"total" example:"64"`
	Limit  int                              `json:"limit" example:"20"`
	Offset int                              `json:"offset" example:"0"`
}

type TotpSetupSwagger struct {
	Secret string `json:"secret" example:"JBSWY3DPEHPK3PXP"`
	QrURL  string `json:"qr_url" example:"otpauth://totp/..."`
}

type ConfirmTotpSwagger struct {
	Code string `json:"code" example:"123456"`
}
