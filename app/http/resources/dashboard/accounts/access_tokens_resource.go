package accounts

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/apitoken"
)

// AccessToken is the access-token row the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps. The token hash stays
// off the wire. A nil page stays nil; an empty page stays empty. A nil token
// stays null.
type AccessToken struct {
	CreatedAt     *carbon.DateTime `json:"created_at"`
	UpdatedAt     *carbon.DateTime `json:"updated_at"`
	ID            uuid.UUID        `json:"id"`
	AccountID     uuid.UUID        `json:"account_id"`
	CreatedBy     *uuid.UUID       `json:"created_by,omitempty"`
	Name          string           `json:"name"`
	Permissions   []string         `json:"permissions,omitempty"`
	IpCidr        string           `json:"ip_cidr,omitempty"`
	SpendingLimit string           `json:"spending_limit,omitempty"`
	ValidUntil    *time.Time       `json:"valid_until,omitempty"`
	LastUsedAt    *time.Time       `json:"last_used_at,omitempty"`
	RevokedAt     *time.Time       `json:"revoked_at,omitempty"`
}

// AccessTokenFrom projects one access token.
func AccessTokenFrom(token models.AccessToken) AccessToken {
	return AccessToken{
		CreatedAt:     token.CreatedAt,
		UpdatedAt:     token.UpdatedAt,
		ID:            token.ID,
		AccountID:     token.AccountID,
		CreatedBy:     token.CreatedBy,
		Name:          token.Name,
		Permissions:   apiTokenPermissionsOnWire(token.Permissions),
		IpCidr:        token.IpCidr,
		SpendingLimit: token.SpendingLimit,
		ValidUntil:    token.ValidUntil,
		LastUsedAt:    token.LastUsedAt,
		RevokedAt:     token.RevokedAt,
	}
}

// AccessTokensFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func AccessTokensFrom(tokens []models.AccessToken) []AccessToken {
	if tokens == nil {
		return nil
	}
	views := make([]AccessToken, len(tokens))
	for i := range tokens {
		views[i] = AccessTokenFrom(tokens[i])
	}
	return views
}

// apiTokenPermissionsOnWire reads the stored JSON array. Empty text and a
// value that is not a JSON array stay off the wire.
func apiTokenPermissionsOnWire(stored string) []string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return nil
	}
	var permissions []string
	if err := json.Unmarshal([]byte(stored), &permissions); err != nil || len(permissions) == 0 {
		return nil
	}
	return permissions
}

// AccessTokenPtr keeps a nil token as JSON null.
func AccessTokenPtr(token *models.AccessToken) *AccessToken {
	if token == nil {
		return nil
	}
	view := AccessTokenFrom(*token)
	return &view
}

// MintedToken is the answer to POST /v1/accounts/{accountId}/tokens: the JWT,
// shown once, and the stored row. The keys keep the order the answer has
// always had.
type MintedToken struct {
	Metadata *AccessToken `json:"metadata"`
	Token    string       `json:"token"`
}

// NewMintedToken shapes a token the token service minted.
func NewMintedToken(minted apitoken.Minted) MintedToken {
	return MintedToken{Metadata: AccessTokenPtr(minted.Token), Token: minted.JWT}
}
