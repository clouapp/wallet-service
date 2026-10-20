package evm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// Two Transfer logs of the registered token in one transaction, the first log of the
// block among them, beside the native value of transaction 0x01.
const twoTransferLogsResult = `"result":[` +
	`{"address":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x0000000000000000000000001111111111111111111111111111111111111111","0x0000000000000000000000002222222222222222222222222222222222222222"],"data":"0x64","transactionHash":"0x02","logIndex":"0x0"},` +
	`{"address":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x0000000000000000000000001111111111111111111111111111111111111111","0x0000000000000000000000002222222222222222222222222222222222222222"],"data":"0x64","transactionHash":"0x02","logIndex":"0x3"}]`

func TestScan_Block_GivesTheNativeValueItsPositionAndEachTokenLogItsLogIndex(t *testing.T) {
	server, _ := logsByHashNode(t, func(map[string]any) string { return twoTransferLogsResult })
	adapter := sepoliaWithFallbacks(server.URL)

	transfers, err := adapter.ScanBlock(context.Background(), 0xaa)
	require.NoError(t, err)
	require.Len(t, transfers, 3)

	native := transfers[0]
	assert.Equal(t, "0x01", native.TxHash)
	assert.Nil(t, native.Token)
	assert.Equal(t, types.NativeValuePosition, native.LogIndex, "the native value has no log: it is not log 0")

	for i, want := range []int{0, 3} {
		token := transfers[1+i]
		assert.Equal(t, "0x02", token.TxHash)
		assert.Equal(t, models.SymbolUSDC, token.Asset)
		assert.Equal(t, want, token.LogIndex)
	}
}

func TestScan_Block_FailsOnATokenLogWithoutAReadableLogIndex(t *testing.T) {
	for name, logIndex := range map[string]string{"missing": "", "not hex": "0xzz", "too large": "0xffffffffffffffff"} {
		t.Run(name, func(t *testing.T) {
			logs := `"result":[{"address":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",` +
				`"0x0000000000000000000000001111111111111111111111111111111111111111","0x0000000000000000000000002222222222222222222222222222222222222222"],` +
				`"data":"0x64","transactionHash":"0x02","logIndex":"` + logIndex + `"}]`
			server, _ := logsByHashNode(t, func(map[string]any) string { return logs })

			transfers, err := sepoliaWithFallbacks(server.URL).ScanBlock(context.Background(), 0xaa)
			require.Error(t, err, "a guessed position could take another transfer's place")
			assert.Nil(t, transfers)
		})
	}
}
