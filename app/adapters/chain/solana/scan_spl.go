package solana

import (
	"fmt"
	"log/slog"
	"math/big"
	"sort"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/pkg/types"
)

// solanaTokenAmount is uiTokenAmount: amount is in base units, decimals are the mint's.
type solanaTokenAmount struct {
	Amount   string `json:"amount"`
	Decimals uint8  `json:"decimals"`
}

// solanaTokenBalance is one entry of meta.preTokenBalances / postTokenBalances: the
// balance of the token account at accountIndex (an index into the account keys), with
// its mint and the wallet that owns it. Token and Token-2022 accounts both appear.
type solanaTokenBalance struct {
	AccountIndex  int               `json:"accountIndex"`
	Mint          string            `json:"mint"`
	Owner         string            `json:"owner"`
	UITokenAmount solanaTokenAmount `json:"uiTokenAmount"`
}

// solanaTokenCredit is the net increase of one registered mint held by one owner in
// one transaction. accountIndex is the owner's lowest token account of that mint after
// the transaction; from is the owner whose holding of that mint fell most, "" when none
// did (a mint).
type solanaTokenCredit struct {
	owner        string
	from         string
	token        types.Token
	amount       *big.Int
	accountIndex int
}

type solanaHolding struct {
	owner string
	mint  string
}

// indexSolanaTokens keys this chain's registered SPL tokens by mint. A mint that is not
// a public key is kept as an error so the scanner fails instead of silently missing its
// deposits.
func indexSolanaTokens(chainID string, tokens []types.Token) (map[string]types.Token, error) {
	index := make(map[string]types.Token, len(tokens))
	for _, token := range tokens {
		if token.ChainID != chainID {
			continue
		}
		if _, err := solana.PublicKeyFromBase58(token.Contract); err != nil {
			return nil, fmt.Errorf("sol token %s mint: %w", token.Symbol, err)
		}
		index[token.Contract] = token
	}
	return index, nil
}

// solanaTokenCredits returns the owners whose total of a registered mint rose in one
// successful transaction, ordered by accountIndex. Balances are netted per owner and
// mint, so moving tokens between two accounts of one owner credits nothing. A
// registered balance that cannot be read is an error.
func solanaTokenCredits(meta *solanaTransactionMeta, keyCount int, tokens map[string]types.Token) ([]solanaTokenCredit, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	net := make(map[solanaHolding]*big.Int)
	firstAccount := make(map[solanaHolding]int)
	chainDecimals := make(map[solanaHolding]uint8)
	add := func(balances []solanaTokenBalance, after bool) error {
		for _, balance := range balances {
			if _, registered := tokens[balance.Mint]; !registered {
				continue
			}
			amount, err := readSolanaTokenBalance(balance, keyCount)
			if err != nil {
				return err
			}
			holding := solanaHolding{owner: balance.Owner, mint: balance.Mint}
			total, seen := net[holding]
			if !seen {
				total = new(big.Int)
				net[holding] = total
			}
			if !after {
				total.Sub(total, amount)
				continue
			}
			total.Add(total, amount)
			chainDecimals[holding] = balance.UITokenAmount.Decimals
			if index, recorded := firstAccount[holding]; !recorded || balance.AccountIndex < index {
				firstAccount[holding] = balance.AccountIndex
			}
		}
		return nil
	}
	if err := add(meta.PreTokenBalances, false); err != nil {
		return nil, err
	}
	if err := add(meta.PostTokenBalances, true); err != nil {
		return nil, err
	}

	var credits []solanaTokenCredit
	for holding, total := range net {
		if total.Sign() <= 0 {
			continue
		}
		token := tokens[holding.mint]
		if token.Decimals != 0 && chainDecimals[holding] != token.Decimals {
			slog.Warn("solana token decimals differ from the registry row; the row is used",
				"mint", holding.mint, "symbol", token.Symbol, "row_decimals", token.Decimals, "mint_decimals", chainDecimals[holding])
		}
		credits = append(credits, solanaTokenCredit{
			owner:        holding.owner,
			from:         largestSolanaDebit(net, holding),
			token:        token,
			amount:       total,
			accountIndex: firstAccount[holding],
		})
	}
	sort.Slice(credits, func(i, j int) bool { return credits[i].accountIndex < credits[j].accountIndex })
	return credits, nil
}

// readSolanaTokenBalance returns the base-unit amount of a registered token balance, or
// an error when its account index, owner or amount cannot be read.
func readSolanaTokenBalance(balance solanaTokenBalance, keyCount int) (*big.Int, error) {
	if balance.AccountIndex < 0 || balance.AccountIndex >= keyCount {
		return nil, fmt.Errorf("token account index %d is outside the %d account keys", balance.AccountIndex, keyCount)
	}
	if balance.Owner == "" {
		return nil, fmt.Errorf("token account %d of mint %s has no owner", balance.AccountIndex, balance.Mint)
	}
	amount, ok := new(big.Int).SetString(balance.UITokenAmount.Amount, 10)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("token account %d of mint %s: amount %q is not a base-unit integer", balance.AccountIndex, balance.Mint, balance.UITokenAmount.Amount)
	}
	return amount, nil
}

// largestSolanaDebit is the other owner whose holding of credited's mint fell most in
// the transaction (ties go to the lower owner), or "" when no holding of it fell.
func largestSolanaDebit(net map[solanaHolding]*big.Int, credited solanaHolding) string {
	var from string
	var largest *big.Int
	for holding, total := range net {
		if holding.mint != credited.mint || holding.owner == credited.owner || total.Sign() >= 0 {
			continue
		}
		if largest == nil || total.Cmp(largest) < 0 || (total.Cmp(largest) == 0 && holding.owner < from) {
			from, largest = holding.owner, total
		}
	}
	return from
}
