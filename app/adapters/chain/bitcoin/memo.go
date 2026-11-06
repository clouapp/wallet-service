package bitcoin

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var _ chain.MemoTransferBuilder = (*BitcoinLive)(nil)

// BuildMemoTransfer uses the regular confirmed UTXO selection and signature
// verification, with a bounded zero-value OP_RETURN included in fee estimation.
func (a *BitcoinLive) BuildMemoTransfer(ctx context.Context, req types.TransferRequest, memo string) (*types.UnsignedTx, error) {
	if len(memo) == 0 || len(memo) > 80 || req.Token != nil || req.Amount == nil || !req.Amount.IsInt64() || req.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("unsupported bitcoin memo transfer")
	}
	if !a.ValidateAddress(req.From) || !a.ValidateAddress(req.To) {
		return nil, fmt.Errorf("invalid bitcoin memo transfer address")
	}
	utxos, err := a.listConfirmedUTXOs(ctx, req.From)
	if err != nil {
		return nil, err
	}
	policy := a.feePolicy(ctx)
	// Output value (8), script length (1), OP_RETURN (1), optional PUSHDATA1,
	// push length (1), and the memo bytes.
	policy.extraVBytes = int64(11 + len(memo))
	if len(memo) > 75 {
		policy.extraVBytes++
	}
	spend, err := selectBTCSpend(utxos, req.Amount.Int64(), policy)
	if err != nil {
		return nil, err
	}
	unsigned := a.unsignedBitcoinTx(req.From, req.To, req.Amount.Int64(), spend)
	outputs, err := outputsFrom(unsigned)
	if err != nil {
		return nil, err
	}
	unsigned.Metadata["outputs"] = append(outputs, btcOutput{Memo: []byte(memo)})
	return unsigned, nil
}
