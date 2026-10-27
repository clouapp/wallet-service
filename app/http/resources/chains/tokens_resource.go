package chains

import "github.com/macrowallets/waas/app/models"

// Token exposes the active token registry's public asset identity and precision.
// It contains no endpoint, credential, account or wallet fields.
type Token struct {
	ChainID         string `json:"chain_id"`
	Symbol          string `json:"symbol"`
	Name            string `json:"name"`
	ContractAddress string `json:"contract_address"`
	Decimals        int    `json:"decimals"`
	Status          string `json:"status"`
}

// ActiveTokensFor keeps the requested chain's active rows. An empty registry
// always serializes as an empty array so consumers can fail closed.
func ActiveTokensFor(chainID string, tokens []models.Token) []Token {
	views := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if token.ChainID != chainID || token.Status != models.TokenStatusActive {
			continue
		}
		views = append(views, Token{ChainID: token.ChainID, Symbol: token.Symbol, Name: token.Name, ContractAddress: token.ContractAddress, Decimals: token.Decimals, Status: token.Status})
	}
	return views
}
