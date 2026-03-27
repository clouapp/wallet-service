package ingest

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/types"
)

func TestProcessTransfers_UnknownChain(t *testing.T) {
	reg := chain.NewRegistry()
	svc := &Service{registry: reg}
	err := svc.ProcessTransfers(t.Context(), "unknown_chain", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain")
}

func TestNewService_NilDeps(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil)
	assert.NotNil(t, svc)
}

// InboundTransfer matches the deposit-scanner DetectedTransfer shape field-for-field
// (LogIndex is int in webhook payloads vs uint in types.DetectedTransfer).
func TestInboundTransfer_DetectedTransferMapping(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	token := &types.Token{Symbol: "USDC", Name: "USD Coin", Contract: "0xtoken", Decimals: 6, ChainID: "ethereum"}
	amount := big.NewInt(42)
	in := providers.InboundTransfer{
		TxHash:      "0xabc",
		BlockNumber: 99,
		BlockHash:   "0xbeef",
		From:        "0xfrom",
		To:          "0xto",
		Amount:      amount,
		Asset:       "ETH",
		Token:       token,
		LogIndex:    7,
		Timestamp:   ts,
	}

	dt := types.DetectedTransfer{
		TxHash:      in.TxHash,
		BlockNumber: in.BlockNumber,
		BlockHash:   in.BlockHash,
		From:        in.From,
		To:          in.To,
		Amount:      in.Amount,
		Asset:       in.Asset,
		Token:       in.Token,
		LogIndex:    uint(in.LogIndex),
		Timestamp:   in.Timestamp,
	}

	assert.Equal(t, in.TxHash, dt.TxHash)
	assert.Equal(t, in.BlockNumber, dt.BlockNumber)
	assert.Equal(t, in.BlockHash, dt.BlockHash)
	assert.Equal(t, in.From, dt.From)
	assert.Equal(t, in.To, dt.To)
	assert.Equal(t, 0, in.Amount.Cmp(dt.Amount))
	assert.Equal(t, in.Asset, dt.Asset)
	assert.Equal(t, in.Token, dt.Token)
	assert.Equal(t, uint(in.LogIndex), dt.LogIndex)
	assert.True(t, in.Timestamp.Equal(dt.Timestamp))
}
