package chain

import (
	"math/big"
	"sort"
)

// The Solana deposit rule, shared by the block scanner (getBlock balances) and the
// Helius push (accountData), so both find the same credits at the same positions.
// A transaction's credits are read from its balance changes, never its instructions:
// lamports per account key, and token balances netted per owner and mint.

// SolanaLamportCredit is a rise of one account's lamports in one transaction, which
// the fee payer is credited as sending. Index is the account's place among the
// transaction's account keys, and the credit's position.
type SolanaLamportCredit struct {
	Account string
	Amount  *big.Int
	Index   int
}

// SolanaLamportCredits returns, in account order, every account other than the fee
// payer (accounts[0]) whose lamports rose; deltas[i] is the change of accounts[i].
func SolanaLamportCredits(accounts []string, deltas []*big.Int) []SolanaLamportCredit {
	if len(accounts) == 0 || len(deltas) != len(accounts) {
		return nil
	}
	feePayer := accounts[0]
	var credits []SolanaLamportCredit
	for i, account := range accounts {
		if account == feePayer || deltas[i] == nil || deltas[i].Sign() <= 0 {
			continue
		}
		credits = append(credits, SolanaLamportCredit{Account: account, Amount: new(big.Int).Set(deltas[i]), Index: i})
	}
	return credits
}

// SolanaTokenChange is a signed change of one token account's balance in one
// transaction, in base units: Index is the token account's place among the account
// keys, Owner the wallet that owns it. A token account may appear twice (a negative
// balance before, a positive one after); the changes add up.
type SolanaTokenChange struct {
	Index int
	Owner string
	Mint  string
	Delta *big.Int
}

// SolanaTokenCredit is the net increase of one mint held by one owner in one
// transaction. Index is the lowest account index among the owner's token accounts of
// that mint whose balance changed; From is the other owner whose holding of the mint
// fell most (ties go to the lower owner), "" when none fell (a mint).
type SolanaTokenCredit struct {
	Owner  string
	Mint   string
	From   string
	Amount *big.Int
	Index  int
}

type solanaHolding struct {
	owner string
	mint  string
}

// SolanaTokenCredits nets changes per owner and mint and returns the holdings that
// rose, ordered by Index. Moving tokens between two accounts of one owner credits
// nothing.
func SolanaTokenCredits(changes []SolanaTokenChange) []SolanaTokenCredit {
	type account struct {
		holding solanaHolding
		index   int
	}
	net := make(map[solanaHolding]*big.Int)
	moved := make(map[account]*big.Int)
	for _, change := range changes {
		if change.Delta == nil {
			continue
		}
		holding := solanaHolding{owner: change.Owner, mint: change.Mint}
		addToHolding(net, holding, change.Delta)
		key := account{holding: holding, index: change.Index}
		if moved[key] == nil {
			moved[key] = new(big.Int)
		}
		moved[key].Add(moved[key], change.Delta)
	}
	lowest := make(map[solanaHolding]int)
	for key, delta := range moved {
		if delta.Sign() == 0 {
			continue
		}
		if index, seen := lowest[key.holding]; !seen || key.index < index {
			lowest[key.holding] = key.index
		}
	}

	var credits []SolanaTokenCredit
	for holding, total := range net {
		if total.Sign() <= 0 {
			continue
		}
		credits = append(credits, SolanaTokenCredit{
			Owner:  holding.owner,
			Mint:   holding.mint,
			From:   largestSolanaDebit(net, holding),
			Amount: total,
			Index:  lowest[holding],
		})
	}
	sort.Slice(credits, func(i, j int) bool { return credits[i].Index < credits[j].Index })
	return credits
}

// SolanaTokenCreditPosition is the position of a token credit: after the
// transaction's accountCount keys, so it never equals a lamport credit's position.
func SolanaTokenCreditPosition(accountCount, index int) int {
	return accountCount + index
}

func addToHolding(totals map[solanaHolding]*big.Int, holding solanaHolding, delta *big.Int) {
	total, seen := totals[holding]
	if !seen {
		total = new(big.Int)
		totals[holding] = total
	}
	total.Add(total, delta)
}

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
