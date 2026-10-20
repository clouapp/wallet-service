package ingest

import (
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

// The scanner recorded log 0 of the transaction and stopped; log 4 pays the same
// address the same amount. The push reports both, so the scanner's row is its log 0
// and log 4 is a deposit of its own.
func TestProcess_Transfers_RecordsTheTwinTheScannerLeftOut(t *testing.T) {
	const to = "0xreceiver"
	reg, addrs, txs := ingestFixture(to)
	txs.created = append(txs.created, &models.Transaction{
		ID: uuid.New(), Chain: models.ChainETH, TxType: models.TxTypeDeposit, TxHash: "0xtwins", LogIndex: 0,
		ToAddress: to, Amount: "5", Source: models.TxSourceChain, BlockNumber: 100,
	})
	svc := NewService(Deps{Registry: reg, AddressRepo: addrs, Transactions: txs})
	pushed := func(logIndex int) providers.InboundTransfer {
		return providers.InboundTransfer{TxHash: "0xtwins", BlockNumber: 100, To: to, From: "0xfrom",
			Amount: big.NewInt(5), Asset: models.NativeETH, LogIndex: logIndex}
	}

	require.NoError(t, svc.ProcessTransfers(t.Context(), models.ChainETH, []providers.InboundTransfer{pushed(0), pushed(4)}))

	require.Len(t, txs.created, 2)
	assert.Equal(t, 4, txs.created[1].LogIndex)
	assert.Equal(t, models.TxSourceDepositIngest, txs.created[1].Source)
}
