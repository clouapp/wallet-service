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
// t-prefixed records are otherwise left alone. The one exception retires the known
// invalid legacy USDT contract on polygon/tpolygon only when their actual network
// is Amoy; it has no bytecode on that network. Mainnet networks remain unchanged.
// Rows are never deleted. With apply false it only reports; a second run changes
// nothing.
func (cat *Catalog) AlignNetworkTokens(ctx context.Context, apply bool) (TokenAlignment, error) {
	if !apply {
		if err := cat.preflightPublicTestUSDT(ctx, false, publicTestAlignment); err != nil {
			return TokenAlignment{}, err
		}
		return cat.alignNetworkTokens(ctx, false)
	}
	var result TokenAlignment
	err := repositories.NewTokenRepository(nil).WithinCatalog(ctx, func(txCtx context.Context) error {
		if err := cat.preflightPublicTestUSDT(txCtx, true, publicTestAlignment); err != nil {
			return err
		}
		var alignErr error
		result, alignErr = cat.alignNetworkTokens(txCtx, true)
		return alignErr
	})
	return result, err
}

func (cat *Catalog) alignNetworkTokens(ctx context.Context, apply bool) (TokenAlignment, error) {
	var result TokenAlignment
	chains := repositories.NewChainRepository(nil)
	tokens := repositories.NewTokenRepository(nil)
	chainIDs := append(append([]string(nil), models.PrimaryChainIDs...), models.ChainTPolygon)
	for _, chainID := range chainIDs {
		retirementOnly := chainID == models.ChainTPolygon
		row, err := chains.FindByID(ctx, chainID)
		if errors.Is(err, models.ErrRepositoryNotFound) {
			if !retirementOnly {
				result.Missing = append(result.Missing, chainID)
			}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("look up chain %s: %w", chainID, err)
		}
		network := row.Network()
		if !models.IsTestnetNetwork(network) {
			if !retirementOnly {
				result.Mainnet = append(result.Mainnet, chainID)
			}
			continue
		}
		rows, err := tokens.FindAllByChainID(ctx, chainID)
		if err != nil {
			return result, err
		}
		var changes []TokenChange
		if retirementOnly {
			for _, token := range rows {
				if retiredAmoyUSDT(chainID, network, token) {
					changes = append(changes, TokenChange{ChainID: chainID, Network: network, Symbol: token.Symbol, Contract: token.ContractAddress, Action: TokenActionDeactivate})
				}
			}
		} else {
			mainnet, _, networkErr := models.PrimaryChainNetwork(models.ChainNetworkProfileMainnet, chainID)
			if networkErr != nil {
				return result, networkErr
			}
			var unknown []string
			changes, unknown = planNetworkTokens(chainID, network, rows, tokensOfNetwork(network), tokensOfNetwork(mainnet.Network))
			result.Unknown = append(result.Unknown, unknown...)
		}
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
		if listsContract(foreign, row.ContractAddress) || retiredAmoyUSDT(chainID, network, row) {
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
			row := findTokenChangeRow(rows, change)
			if row == nil {
				return fmt.Errorf("token %s %s vanished during the alignment", change.ChainID, change.Contract)
			}
			if change.Action == TokenActionDeactivate && retiredAmoyUSDT(change.ChainID, change.Network, *row) {
				if retirementErr := tokens.RetireInvalidAmoyUSDT(ctx, row.ID, change.ChainID, row.ContractAddress); retirementErr != nil {
					return retirementErr
				}
				slog.Info("invalid Amoy token retired", "chain_id", change.ChainID, "symbol", change.Symbol)
				continue
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

// This exact legacy test token has no code on Amoy. Do not generalize this
// retirement to unknown custom tokens, another symbol or another physical network.
const retiredAmoyUSDTContract = repositories.InvalidAmoyUSDTContract

func retiredAmoyUSDT(chainID, network string, token models.Token) bool {
	return (chainID == models.ChainPolygon || chainID == models.ChainTPolygon) && network == models.NetworkPolygonAmoy && token.ChainID == chainID && token.Symbol == models.SymbolUSDT && token.Status == models.TokenStatusActive && sameContract(token.ContractAddress, retiredAmoyUSDTContract)
}
func findTokenChangeRow(rows []models.Token, change TokenChange) *models.Token {
	for i := range rows {
		if rows[i].ChainID == change.ChainID && rows[i].Symbol == change.Symbol && rows[i].ContractAddress == change.Contract {
			return &rows[i]
		}
	}
	return nil
}
