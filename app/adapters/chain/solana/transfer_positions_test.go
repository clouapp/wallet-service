package solana

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/solanacredit"
)

// The credit's position is the lowest token account of the owner and mint whose
// balance changed: an account the transaction lists but does not move is left out,
// as a provider push that lists the changed accounts only leaves it out.
func TestSolana_BlockCredits_ATokenCreditIsNumberedByItsLowestChangedAccount(t *testing.T) {
	keys := []string{"payer", "ataIdle", "ataSender", "ataPaid"}
	block := tokenBlock(keys,
		[]solanaTokenBalance{usdcBalance(1, "owner", "10"), usdcBalance(2, "sender", "9"), usdcBalance(3, "owner", "0")},
		[]solanaTokenBalance{usdcBalance(1, "owner", "10"), usdcBalance(2, "sender", "2"), usdcBalance(3, "owner", "7")}, false)

	got, err := solanaBlockCredits(1, block, models.NativeSOL, usdcIndex())
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, "owner", got[0].To)
	assert.Equal(t, "sender", got[0].From)
	assert.Equal(t, int64(7), got[0].Amount.Int64())
	assert.Equal(t, solanacredit.TokenPosition(3), got[0].LogIndex)
}
