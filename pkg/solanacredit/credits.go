// Package solanacredit is the Solana deposit rule, shared by the block scanner
// (getBlock balances) and the Helius push (accountData), so both find the same credits
// at the same positions. A transaction's credits are read from its balance changes,
// never its instructions: lamports per account key, and token balances netted per
// owner and mint.
package solanacredit

import (
	"math/big"
	"sort"
)

// MaxAccounts is the most account keys a transaction can reference (static and
// lookup-table loaded): account indexes are one byte.
const MaxAccounts = 256

// Lamport is a rise of one account's lamports in one transaction, which the fee payer
// is credited as sending. Index is the account's place among the transaction's
// account keys, and the credit's position.
type Lamport struct {
	Account string
	Amount  *big.Int
	Index   int
}

// Lamports returns, in account order, every account other than the fee payer
// (accounts[0]) whose lamports rose; deltas[i] is the change of accounts[i].
func Lamports(accounts []string, deltas []*big.Int) []Lamport {
	if len(accounts) == 0 || len(deltas) != len(accounts) {
		return nil
	}
	feePayer := accounts[0]
	var credits []Lamport
	for i, account := range accounts {
		if account == feePayer || deltas[i] == nil || deltas[i].Sign() <= 0 {
			continue
		}
		credits = append(credits, Lamport{Account: account, Amount: new(big.Int).Set(deltas[i]), Index: i})
	}
	return credits
}

// TokenChange is a signed change of one token account's balance in one transaction,
// in base units: Index is the token account's place among the account keys, Owner the
// wallet that owns it. A token account may appear twice (a negative balance before, a
// positive one after); the changes add up.
type TokenChange struct {
	Index int
	Owner string
	Mint  string
	Delta *big.Int
}

// Token is the net increase of one mint held by one owner in one transaction. Index
// is the lowest account index among the owner's token accounts of that mint whose
// balance changed; From is the other owner whose holding of the mint fell most (ties
// go to the lower owner), "" when none fell (a mint).
type Token struct {
	Owner  string
	Mint   string
	From   string
	Amount *big.Int
	Index  int
}

type holding struct {
	owner string
	mint  string
}

// Tokens nets changes per owner and mint and returns the holdings that rose, ordered
// by Index. Moving tokens between two accounts of one owner credits nothing.
func Tokens(changes []TokenChange) []Token {
	type account struct {
		holding holding
		index   int
	}
	net := make(map[holding]*big.Int)
	moved := make(map[account]*big.Int)
	for _, change := range changes {
		if change.Delta == nil {
			continue
		}
		key := holding{owner: change.Owner, mint: change.Mint}
		addTo(net, key, change.Delta)
		at := account{holding: key, index: change.Index}
		if moved[at] == nil {
			moved[at] = new(big.Int)
		}
		moved[at].Add(moved[at], change.Delta)
	}
	lowest := make(map[holding]int)
	for at, delta := range moved {
		if delta.Sign() == 0 {
			continue
		}
		if index, seen := lowest[at.holding]; !seen || at.index < index {
			lowest[at.holding] = at.index
		}
	}

	var credits []Token
	for key, total := range net {
		if total.Sign() <= 0 {
			continue
		}
		credits = append(credits, Token{
			Owner:  key.owner,
			Mint:   key.mint,
			From:   largestDebit(net, key),
			Amount: total,
			Index:  lowest[key],
		})
	}
	sort.Slice(credits, func(i, j int) bool { return credits[i].Index < credits[j].Index })
	return credits
}

// TokenPosition is the position of a token credit whose lowest changed token account
// is at index: after every possible account index, so it never equals a lamport
// credit's position and does not depend on how many accounts a source lists.
func TokenPosition(index int) int {
	return MaxAccounts + index
}

func addTo(totals map[holding]*big.Int, key holding, delta *big.Int) {
	total, seen := totals[key]
	if !seen {
		total = new(big.Int)
		totals[key] = total
	}
	total.Add(total, delta)
}

func largestDebit(net map[holding]*big.Int, credited holding) string {
	var from string
	var largest *big.Int
	for key, total := range net {
		if key.mint != credited.mint || key.owner == credited.owner || total.Sign() >= 0 {
			continue
		}
		if largest == nil || total.Cmp(largest) < 0 || (total.Cmp(largest) == 0 && key.owner < from) {
			from, largest = key.owner, total
		}
	}
	return from
}
