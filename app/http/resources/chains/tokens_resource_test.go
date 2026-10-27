package chains_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/http/resources/chains"
	"github.com/macrowallets/waas/app/models"
)

func TestActiveTokenMetadataKeepsActualPrecisionAndEmptyArrays(t *testing.T) {
	for _, precision := range []int{0, 6, 9, 18} {
		rows := chains.ActiveTokensFor("sol", []models.Token{{ChainID: "sol", Symbol: "TST", Name: "Test asset", ContractAddress: "public-mint", Decimals: precision, Status: "active"}})
		require.Len(t, rows, 1)
		require.Equal(t, precision, rows[0].Decimals)
	}
	data, err := json.Marshal(chains.ActiveTokensFor("sol", nil))
	require.NoError(t, err)
	require.Equal(t, "[]", string(data))
}
