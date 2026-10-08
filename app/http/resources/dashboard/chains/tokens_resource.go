package chains

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// Token is the chain token the dashboard reads. Field order and tags
// match the model wire, including embedded timestamps. A nil page stays nil;
// an empty page stays empty. A non-nil empty icon URL stays "".
type Token struct {
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

// TokenFrom projects one chain token.
func TokenFrom(token models.Token) Token {
	return Token{
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

// TokensFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func TokensFrom(tokens []models.Token) []Token {
	if tokens == nil {
		return nil
	}
	views := make([]Token, len(tokens))
	for i := range tokens {
		views[i] = TokenFrom(tokens[i])
	}
	return views
}
