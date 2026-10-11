package ingest

import (
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestProcess_Transfers_RecordsTheChainFinalityPolicy(t *testing.T) {
	const to = "0xReceiver"
	reg := chain.NewRegistry()
	mockChain := mocks.NewMockChain(models.ChainBSC)
	mockChain.NativeAssetVal = models.NativeBNB
	mockChain.RequiredConfirmationsVal = 2
	mockChain.FinalityPolicyVal = models.FinalityFinalizedTag
	reg.RegisterChain(mockChain)
	addrs := &ingestAddressRepo{addr: &models.Address{
		ID: uuid.New(), WalletID: uuid.New(), ExternalUserID: "user-1", Chain: models.ChainBSC, Address: to,
	}}
	txs := &ingestTxRepo{}
	svc := NewService(Deps{Registry: reg, AddressRepo: addrs, Transactions: txs})

	err := svc.processTransfer(t.Context(), models.ChainBSC, mockChain, providers.InboundTransfer{
		TxHash: "0xbscdeposit", BlockNumber: 100, To: to, From: "0xfrom", Amount: big.NewInt(5), Asset: models.NativeBNB, LogIndex: -1,
	})
	require.NoError(t, err)
	require.Len(t, txs.created, 1)
	assert.Equal(t, models.FinalityFinalizedTag, txs.created[0].Finality)
	assert.Equal(t, 2, txs.created[0].RequiredConfs)
}
