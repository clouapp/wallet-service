package auth

import (
	userresource "github.com/macrowallets/waas/app/http/resources/dashboard/users"
)

// The types below document request and response bodies in the swagger
// annotations of this package. Handlers do not use them.

type RegisterSwagger struct {
	Email            string `json:"email" example:"user@example.com"`
	Password         string `json:"password" example:"s3cr3t"`
	FullName         string `json:"full_name" example:"Alice Smith"`
	OrganizationName string `json:"organization_name" example:"Acme Corp"`
}

type LoginSwagger struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"s3cr3t"`
}

type TwoFactorSwagger struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code" example:"123456"`
	RecoveryCode   string `json:"recovery_code" example:"ABCDEFGH12345678"`
}

type RefreshTokenSwagger struct {
	RefreshToken string `json:"refresh_token"`
}

type ForgotPasswordSwagger struct {
	Email string `json:"email" example:"user@example.com"`
}

type ResetPasswordSwagger struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password" example:"newS3cr3t"`
}

type AuthResponse struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	User         userresource.User `json:"user,omitempty"`
}
