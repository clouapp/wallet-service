package accounts

import (
	"time"

	resources "github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
)

// The types below document request and response bodies in the swagger
// annotations of this package. Handlers do not use them.

type CreateAccountSwagger struct {
	Name string `json:"name" example:"Acme Corp"`
}

type UpdateAccountSwagger struct {
	Name           string `json:"name,omitempty" example:"New Name"`
	ViewAllWallets *bool  `json:"view_all_wallets,omitempty" example:"true"`
}

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

type AddAccountUserSwagger struct {
	Email string `json:"email" example:"user@example.com"`
	Role  string `json:"role" example:"admin"`
}

type UpdateAccountUserSwagger struct {
	Role   string `json:"role,omitempty" example:"admin"`
	Status string `json:"status,omitempty" example:"suspended"`
}

type AccountUserListResponse struct {
	Data []resources.AccountUser `json:"data"`
}
