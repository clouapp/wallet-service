package solana

import (
	"fmt"
	"log/slog"
	"math/big"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/pkg/solanacredit"
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
// one transaction (solanacredit.Tokens): accountIndex is the owner's lowest token
// account of that mint whose balance changed; from is the owner whose holding of that
// mint fell most, "" when none did (a mint).
type solanaTokenCredit struct {
	owner        string
	from         string
	token        types.Token
	amount       *big.Int
	accountIndex int
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
// successful transaction, ordered by accountIndex: each registered balance before the
// transaction is a negative change of its token account and each balance after it a
// positive one (solanacredit.Tokens nets them per owner and mint). A registered
// balance that cannot be read is an error.
func solanaTokenCredits(meta *solanaTransactionMeta, keyCount int, tokens map[string]types.Token) ([]solanaTokenCredit, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	var changes []solanacredit.TokenChange
	chainDecimals := make(map[string]uint8)
	collect := func(balances []solanaTokenBalance, after bool) error {
		for _, balance := range balances {
			if _, registered := tokens[balance.Mint]; !registered {
				continue
			}
			amount, err := readSolanaTokenBalance(balance, keyCount)
			if err != nil {
				return err
			}
			if after {
				chainDecimals[balance.Mint] = balance.UITokenAmount.Decimals
			} else {
				amount.Neg(amount)
			}
			changes = append(changes, solanacredit.TokenChange{Index: balance.AccountIndex, Owner: balance.Owner, Mint: balance.Mint, Delta: amount})
		}
		return nil
	}
	if err := collect(meta.PreTokenBalances, false); err != nil {
		return nil, err
	}
	if err := collect(meta.PostTokenBalances, true); err != nil {
		return nil, err
	}

	var credits []solanaTokenCredit
	for _, c := range solanacredit.Tokens(changes) {
		token := tokens[c.Mint]
		if decimals, read := chainDecimals[c.Mint]; read && token.Decimals != 0 && decimals != token.Decimals {
			slog.Warn("solana token decimals differ from the registry row; the row is used",
				"mint", c.Mint, "symbol", token.Symbol, "row_decimals", token.Decimals, "mint_decimals", decimals)
		}
		credits = append(credits, solanaTokenCredit{owner: c.Owner, from: c.From, token: token, amount: c.Amount, accountIndex: c.Index})
	}
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
