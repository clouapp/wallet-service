package chains

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// TokenView is the chain token the dashboard reads. Field order and tags
// match the model wire, including embedded timestamps. A nil page stays nil;
// an empty page stays empty. A non-nil empty icon URL stays "".
type TokenView struct {
	CreatedAt       *carbon.DateTime `json:"created_at"`
	UpdatedAt       *carbon.DateTime `json:"updated_at"`
	ID              uuid.UUID        `json:"id"`
	ChainID         string           `json:"chain_id"`
	Symbol          string           `json:"symbol"`
	Name            string           `json:"name"`
	ContractAddress string           `json:"contract_address"`
	Decimals        int              `json:"decimals"`
	IconURL         *string          `json:"icon_url,omitempty"`
	Status          string           `json:"status"`
}

func newTokenView(token models.Token) TokenView {
	return TokenView{
		CreatedAt:       token.CreatedAt,
		UpdatedAt:       token.UpdatedAt,
		ID:              token.ID,
		ChainID:         token.ChainID,
		Symbol:          token.Symbol,
		Name:            token.Name,
		ContractAddress: token.ContractAddress,
		Decimals:        token.Decimals,
		IconURL:         token.IconURL,
		Status:          token.Status,
	}
}

func tokenViews(tokens []models.Token) []TokenView {
	if tokens == nil {
		return nil
	}
	views := make([]TokenView, len(tokens))
	for i := range tokens {
		views[i] = newTokenView(tokens[i])
	}
	return views
}
