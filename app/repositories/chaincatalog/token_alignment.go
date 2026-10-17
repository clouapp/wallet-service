package chaincatalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// What AlignNetworkTokens does to one token row.
const (
	TokenActionCreate     = "create"
	TokenActionActivate   = "activate"
	TokenActionDeactivate = "deactivate"
)

// TokenChange is one token row AlignNetworkTokens creates, re-activates or deactivates.
type TokenChange struct {
	ChainID  string
	Network  string
	Symbol   string
	Contract string
	Action   string
}

// TokenAlignment is what AlignNetworkTokens changed, or would change. Unknown lists the
// active tokens of an aligned record that no catalog list names ("chain:symbol:contract"),
// left as they are; Mainnet the primary records on a mainnet network and Missing the
// ones the registry lacks, all left alone.
type TokenAlignment struct {
	Changes []TokenChange
	Unknown []string
	Mainnet []string
	Missing []string
}

// AlignNetworkTokens moves the tokens of every primary record that points at a test
// network onto the catalog's tokens of that network: a listed token the record lacks is
// created, an inactive one re-activated, and an active token of the record's mainnet
// list (the contract of another network) deactivated. The network is the one the row
// stores (network_id, is_testnet), not the profile. A record on a mainnet network and
// every t-prefixed record are never touched, so a mainnet database is left as it is.
// Rows are never deleted. With apply false it only reports; a second run changes
// nothing.
func (cat *Catalog) AlignNetworkTokens(ctx context.Context, apply bool) (TokenAlignment, error) {
	var result TokenAlignment
	chains := repositories.NewChainRepository(nil)
	tokens := repositories.NewTokenRepository(nil)
	for _, chainID := range models.PrimaryChainIDs {
		row, err := chains.FindByID(ctx, chainID)
		if errors.Is(err, models.ErrRepositoryNotFound) {
			result.Missing = append(result.Missing, chainID)
			continue
		}
		if err != nil {
			return result, fmt.Errorf("look up chain %s: %w", chainID, err)
		}
		network := row.Network()
		if !models.IsTestnetNetwork(network) {
			result.Mainnet = append(result.Mainnet, chainID)
			continue
		}
		mainnet, _, err := models.PrimaryChainNetwork(models.ChainNetworkProfileMainnet, chainID)
		if err != nil {
			return result, err
		}
		rows, err := tokens.FindAllByChainID(ctx, chainID)
		if err != nil {
			return result, err
		}
		changes, unknown := planNetworkTokens(chainID, network, rows, tokensOfNetwork(network), tokensOfNetwork(mainnet.Network))
		result.Unknown = append(result.Unknown, unknown...)
		if apply {
			if err := applyTokenChanges(ctx, tokens, rows, changes); err != nil {
				return result, err
			}
		}
		result.Changes = append(result.Changes, changes...)
	}
	return result, nil
}

// planNetworkTokens compares a record's token rows with its network's list (expected)
// and the list of its mainnet network (foreign) and returns the changes and the active
// rows neither list names.
func planNetworkTokens(chainID, network string, rows []models.Token, expected, foreign []tokenSeed) ([]TokenChange, []string) {
	var changes []TokenChange
	for _, token := range expected {
		row := findTokenRow(rows, token.contractAddress)
		switch {
		case row == nil:
			changes = append(changes, TokenChange{ChainID: chainID, Network: network, Symbol: token.symbol, Contract: token.contractAddress, Action: TokenActionCreate})
		case row.Status != models.TokenStatusActive:
			changes = append(changes, TokenChange{ChainID: chainID, Network: network, Symbol: row.Symbol, Contract: row.ContractAddress, Action: TokenActionActivate})
		}
	}
	var unknown []string
	for _, row := range rows {
		if row.Status != models.TokenStatusActive || listsContract(expected, row.ContractAddress) {
			continue
		}
		if listsContract(foreign, row.ContractAddress) {
			changes = append(changes, TokenChange{ChainID: chainID, Network: network, Symbol: row.Symbol, Contract: row.ContractAddress, Action: TokenActionDeactivate})
			continue
		}
		unknown = append(unknown, chainID+":"+row.Symbol+":"+row.ContractAddress)
	}
	return changes, unknown
}

func applyTokenChanges(ctx context.Context, tokens *repositories.TokenRepository, rows []models.Token, changes []TokenChange) error {
	for _, change := range changes {
		switch change.Action {
		case TokenActionCreate:
			seed, found := findNetworkToken(change.Network, change.Contract)
			if !found {
				return fmt.Errorf("token %s %s is not in the %s list", change.ChainID, change.Contract, change.Network)
			}
			seed.chainID = change.ChainID
			if _, err := insertTokenUnlessPresent(ctx, tokens, seed); err != nil {
				return err
			}
		case TokenActionActivate, TokenActionDeactivate:
			row := findTokenRow(rows, change.Contract)
			if row == nil {
				return fmt.Errorf("token %s %s vanished during the alignment", change.ChainID, change.Contract)
			}
			status := models.TokenStatusActive
			if change.Action == TokenActionDeactivate {
				status = models.TokenStatusInactive
			}
			if err := tokens.SetStatus(ctx, row.ID, status); err != nil {
				return fmt.Errorf("%s token %s %s: %w", change.Action, change.ChainID, change.Symbol, err)
			}
			slog.Info("token status changed", "chain_id", change.ChainID, "symbol", change.Symbol, "status", status)
		}
	}
	return nil
}

func findNetworkToken(network, contract string) (tokenSeed, bool) {
	for _, token := range tokensOfNetwork(network) {
		if sameContract(token.contractAddress, contract) {
			return token, true
		}
	}
	return tokenSeed{}, false
}

func findTokenRow(rows []models.Token, contract string) *models.Token {
	for i := range rows {
		if sameContract(rows[i].ContractAddress, contract) {
			return &rows[i]
		}
	}
	return nil
}

func listsContract(tokens []tokenSeed, contract string) bool {
	for _, token := range tokens {
		if sameContract(token.contractAddress, contract) {
			return true
		}
	}
	return false
}

// sameContract compares EVM (0x) addresses case-insensitively, since checksum casing
// is optional, and every other encoding (base58 mints, TRON addresses) exactly.
func sameContract(a, b string) bool {
	if strings.HasPrefix(a, "0x") && strings.HasPrefix(b, "0x") {
		return strings.EqualFold(a, b)
	}
	return a == b
}
