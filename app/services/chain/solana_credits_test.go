package chain

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	usdcMint = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	wsolMint = "So11111111111111111111111111111111111111112"
)

func change(index int, owner, mint string, delta int64) SolanaTokenChange {
	return SolanaTokenChange{Index: index, Owner: owner, Mint: mint, Delta: big.NewInt(delta)}
}

func TestSolana_TokenCredits_NetsEachOwnerAndMintAndNamesTheLargestDebit(t *testing.T) {
	credits := SolanaTokenCredits([]SolanaTokenChange{
		change(5, "seller", usdcMint, -2_000),
		change(6, "router", usdcMint, -200),
		change(3, "buyer", usdcMint, 1_500),
		change(9, "buyer", usdcMint, 700),
		change(4, "buyer", wsolMint, 6_010),
	})

	require.Len(t, credits, 2)
	assert.Equal(t, SolanaTokenCredit{Owner: "buyer", Mint: usdcMint, From: "seller", Amount: big.NewInt(2_200), Index: 3}, credits[0])
	assert.Equal(t, SolanaTokenCredit{Owner: "buyer", Mint: wsolMint, From: "", Amount: big.NewInt(6_010), Index: 4}, credits[1],
		"a mint whose holdings did not fall anywhere was minted: no sender")
}

func TestSolana_TokenCredits_MovingBetweenOneOwnersAccountsCreditsNothing(t *testing.T) {
	credits := SolanaTokenCredits([]SolanaTokenChange{
		change(1, "owner", usdcMint, -5),
		change(2, "owner", usdcMint, 5),
	})
	assert.Empty(t, credits)
}

// The position must not depend on accounts whose balance did not move: a provider
// push lists the changed token accounts only.
func TestSolana_TokenCredits_IndexIsTheLowestChangedAccountOfTheHolding(t *testing.T) {
	credits := SolanaTokenCredits([]SolanaTokenChange{
		// Account 1 holds 10 before and after: listed by the node, but unchanged.
		change(1, "owner", usdcMint, -10),
		change(1, "owner", usdcMint, 10),
		change(7, "owner", usdcMint, 3),
		change(3, "owner", usdcMint, 4),
	})

	require.Len(t, credits, 1)
	assert.Equal(t, 3, credits[0].Index)
	assert.Equal(t, big.NewInt(7), credits[0].Amount)
}

func TestSolana_TokenCredits_AreOrderedByIndex(t *testing.T) {
	credits := SolanaTokenCredits([]SolanaTokenChange{
		change(8, "b", usdcMint, 1),
		change(6, "a", usdcMint, 2),
		change(2, "c", usdcMint, -3),
	})

	require.Len(t, credits, 2)
	assert.Equal(t, []string{"a", "b"}, []string{credits[0].Owner, credits[1].Owner})
	assert.Equal(t, "c", credits[0].From)
}

func TestSolana_TokenCreditPosition_ComesAfterEveryAccountKey(t *testing.T) {
	assert.Equal(t, 28, SolanaTokenCreditPosition(25, 3))
}

func TestSolana_LamportCredits_EveryRiseButTheFeePayersInAccountOrder(t *testing.T) {
	accounts := []string{"payer", "alice", "bob", "carol"}
	deltas := []*big.Int{big.NewInt(-10_000), big.NewInt(5_000), big.NewInt(0), big.NewInt(3)}

	credits := SolanaLamportCredits(accounts, deltas)

	assert.Equal(t, []SolanaLamportCredit{
		{Account: "alice", Amount: big.NewInt(5_000), Index: 1},
		{Account: "carol", Amount: big.NewInt(3), Index: 3},
	}, credits)
}

func TestSolana_LamportCredits_TheFeePayerIsNeverCredited(t *testing.T) {
	credits := SolanaLamportCredits([]string{"payer", "payer"}, []*big.Int{big.NewInt(1), big.NewInt(1)})
	assert.Empty(t, credits, "an account key repeated as the fee payer is the fee payer")
}
