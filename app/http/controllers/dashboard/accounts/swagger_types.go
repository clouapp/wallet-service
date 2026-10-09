package accounts

import (
	"time"

	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
)

// The types below document request and response bodies in the swagger
// annotations of this package. Handlers do not use them.

type CreateAccountTokenSwagger struct {
	Name             string     `json:"name" example:"CI Token"`
	ValidUntil       *time.Time `json:"valid_until,omitempty"`
	RequireSignature bool       `json:"require_signature,omitempty" example:"true"`
	Permissions      []string   `json:"permissions,omitempty"`
	IpCidr           string     `json:"ip_cidr,omitempty" example:"192.0.2.0/24"`
}

type AccessTokenListResponse struct {
	Data []resources.AccessToken `json:"data"`
}

type CreateAccountTokenResponse struct {
	Token    string                `json:"token"`
	Metadata resources.AccessToken `json:"metadata"`
}
