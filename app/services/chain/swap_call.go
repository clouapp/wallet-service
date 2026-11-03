package chain

import (
	"context"
	"math/big"

	"github.com/macrowallets/waas/pkg/types"
)

// SwapCallBuilder simulates and prices a call using the wallet's registered RPC.
// Provider supplied nonce, gas and fee values are deliberately not used.
type SwapCallBuilder interface {
	PrepareSwapCall(context.Context, string, string, *big.Int, []byte) (*types.UnsignedTx, error)
	SwapAllowance(context.Context, string, string, string) (*big.Int, error)
}

// MemoTransferBuilder builds a native deposit with its required protocol memo.
type MemoTransferBuilder interface {
	BuildMemoTransfer(context.Context, types.TransferRequest, string) (*types.UnsignedTx, error)
}

// SolanaSwapBuilder verifies a frozen provider message against live account
// simulation before the custody service is allowed to sign it.
type SolanaSwapBuilder interface {
	PrepareSolanaSwap(context.Context, string, string, *big.Int, *types.Token) (*types.UnsignedTx, error)
}
