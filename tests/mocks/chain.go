package mocks

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/macrowallets/waas/pkg/types"
)

// ---------------------------------------------------------------------------
// MockChain — configurable mock for types.Chain interface.
// Every method can be overridden via function fields.
// ---------------------------------------------------------------------------

type MockChain struct {
	IDVal                  string
	NameVal                string
	NativeAssetVal         string
	RequiredConfirmationsVal uint64

	DeriveAddressFn        func(masterKey []byte, index uint32) (string, error)
	ValidateAddressFn      func(address string) bool
	GetBalanceFn           func(ctx context.Context, address string) (*types.Balance, error)
	GetTokenBalanceFn      func(ctx context.Context, address string, token types.Token) (*types.Balance, error)
	BuildTransferFn        func(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error)
	SignTransactionFn      func(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error)
	BroadcastTransactionFn func(ctx context.Context, signed *types.SignedTx) (string, error)
	GetLatestBlockFn       func(ctx context.Context) (uint64, error)
	ScanBlockFn            func(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error)

	// Call tracking
	DeriveAddressCalls        int
	ValidateAddressCalls      int
	BuildTransferCalls        int
	SignTransactionCalls      int
	BroadcastTransactionCalls int
	ScanBlockCalls            int
}

func NewMockChain(id string) *MockChain {
	return &MockChain{
		IDVal:                    id,
		NameVal:                  "Mock " + id,
		NativeAssetVal:           id,
		RequiredConfirmationsVal: 3,
	}
}

func (m *MockChain) ID() string                   { return m.IDVal }
func (m *MockChain) Name() string                 { return m.NameVal }
func (m *MockChain) NativeAsset() string           { return m.NativeAssetVal }
func (m *MockChain) RequiredConfirmations() uint64 { return m.RequiredConfirmationsVal }

func (m *MockChain) DeriveAddress(masterKey []byte, index uint32) (string, error) {
	m.DeriveAddressCalls++
	if m.DeriveAddressFn != nil {
		return m.DeriveAddressFn(masterKey, index)
	}
	return fmt.Sprintf("0xmock%s%04d", m.IDVal, index), nil
}

func (m *MockChain) ValidateAddress(address string) bool {
	m.ValidateAddressCalls++
	if m.ValidateAddressFn != nil {
		return m.ValidateAddressFn(address)
	}
	return len(address) > 5
}

func (m *MockChain) GetBalance(ctx context.Context, address string) (*types.Balance, error) {
	if m.GetBalanceFn != nil {
		return m.GetBalanceFn(ctx, address)
	}
	return &types.Balance{Address: address, Asset: m.NativeAssetVal, Amount: big.NewInt(1000000), Decimals: 18, Human: "0.000001"}, nil
}

func (m *MockChain) GetTokenBalance(ctx context.Context, address string, token types.Token) (*types.Balance, error) {
	if m.GetTokenBalanceFn != nil {
		return m.GetTokenBalanceFn(ctx, address, token)
	}
	return &types.Balance{Address: address, Asset: token.Symbol, Amount: big.NewInt(500000), Decimals: token.Decimals, Human: "0.5"}, nil
}

func (m *MockChain) BuildTransfer(ctx context.Context, req types.TransferRequest) (*types.UnsignedTx, error) {
	m.BuildTransferCalls++
	if m.BuildTransferFn != nil {
		return m.BuildTransferFn(ctx, req)
	}
	return &types.UnsignedTx{ChainID: m.IDVal, RawBytes: []byte("unsigned"), Metadata: map[string]interface{}{"nonce": 0}}, nil
}

func (m *MockChain) SignTransaction(ctx context.Context, unsigned *types.UnsignedTx, privateKey []byte) (*types.SignedTx, error) {
	m.SignTransactionCalls++
	if m.SignTransactionFn != nil {
		return m.SignTransactionFn(ctx, unsigned, privateKey)
	}
	return &types.SignedTx{ChainID: m.IDVal, TxHash: "0xmockhash123", RawBytes: []byte("signed")}, nil
}

func (m *MockChain) BroadcastTransaction(ctx context.Context, signed *types.SignedTx) (string, error) {
	m.BroadcastTransactionCalls++
	if m.BroadcastTransactionFn != nil {
		return m.BroadcastTransactionFn(ctx, signed)
	}
	return "0xbroadcasthash456", nil
}

func (m *MockChain) GetLatestBlock(ctx context.Context) (uint64, error) {
	if m.GetLatestBlockFn != nil {
		return m.GetLatestBlockFn(ctx)
	}
	return 1000, nil
}

func (m *MockChain) ScanBlock(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error) {
	m.ScanBlockCalls++
	if m.ScanBlockFn != nil {
		return m.ScanBlockFn(ctx, blockNum)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// MockSQS — captures messages sent to queues
// ---------------------------------------------------------------------------

type MockSQS struct {
	WebhookMessages []types.WebhookMessage
	SendWebhookErr  error
}

func NewMockSQS() *MockSQS {
	return &MockSQS{}
}

func (m *MockSQS) SendWebhook(ctx context.Context, msg types.WebhookMessage) error {
	if m.SendWebhookErr != nil {
		return m.SendWebhookErr
	}
	m.WebhookMessages = append(m.WebhookMessages, msg)
	return nil
}

// ---------------------------------------------------------------------------
// Transfer helpers for building test data
// ---------------------------------------------------------------------------

func MakeTransfer(txHash, from, to string, amount int64, asset string) types.DetectedTransfer {
	return types.DetectedTransfer{
		TxHash:      txHash,
		BlockNumber: 100,
		BlockHash:   "0xblockhash",
		From:        from,
		To:          to,
		Amount:      big.NewInt(amount),
		Asset:       asset,
		Timestamp:   time.Now(),
	}
}

func MakeTokenTransfer(txHash, from, to string, amount int64, token types.Token) types.DetectedTransfer {
	t := MakeTransfer(txHash, from, to, amount, token.Symbol)
	t.Token = &token
	return t
}
