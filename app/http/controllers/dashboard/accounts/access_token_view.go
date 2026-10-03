package accounts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// AccessTokenView is the access-token row the dashboard reads. Field order and
// tags match the model wire, including embedded timestamps. The token hash stays
// off the wire. A nil page stays nil; an empty page stays empty. A nil token
// stays null.
type AccessTokenView struct {
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

func newAccessTokenView(token models.AccessToken) AccessTokenView {
	return AccessTokenView{
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

// AccessTokenViews copies a page. A nil slice stays nil; an empty slice stays empty.
func AccessTokenViews(tokens []models.AccessToken) []AccessTokenView {
	if tokens == nil {
		return nil
	}
	views := make([]AccessTokenView, len(tokens))
	for i := range tokens {
		views[i] = newAccessTokenView(tokens[i])
	}
	return views
}

// storedAPITokenPermissions keeps an empty grant as the column's empty
// text. A non-empty grant is the JSON array the plan stores.
func storedAPITokenPermissions(permissions []string) (string, error) {
	if len(permissions) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(permissions)
	if err != nil {
		return "", fmt.Errorf("encode api token permissions: %w", err)
	}
	return string(raw), nil
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

// AccessTokenViewPtr keeps a nil token as JSON null.
func AccessTokenViewPtr(token *models.AccessToken) *AccessTokenView {
	if token == nil {
		return nil
	}
	view := newAccessTokenView(*token)
	return &view
}
