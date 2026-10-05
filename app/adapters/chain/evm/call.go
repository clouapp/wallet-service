package evm

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// BuildCall encodes call for this adapter's network (EIP-155 with NetworkID) and
// returns it with its signing hash, ready for FinalizeMPCSignature and
// VerifySignedTransaction.
func (a *EVMLive) BuildCall(call chain.EVMCall) (*types.UnsignedTx, error) {
	if a.cfg.NetworkID <= 0 {
		return nil, fmt.Errorf("evm call: adapter %s has no network id", a.cfg.ChainIDStr)
	}
	if !common.IsHexAddress(call.To) {
		return nil, fmt.Errorf("evm call: destination %q is not an EVM address", call.To)
	}
	if call.Value == nil || call.Value.Sign() < 0 {
		return nil, fmt.Errorf("evm call: value must be zero or positive")
	}
	if call.GasLimit == 0 {
		return nil, fmt.Errorf("evm call: gas limit must be positive")
	}
	if call.GasPrice == nil || call.GasPrice.Sign() <= 0 {
		return nil, fmt.Errorf("evm call: gas price must be positive")
	}

	unsigned := &types.UnsignedTx{
		ChainID: a.cfg.ChainIDStr,
		Metadata: map[string]interface{}{
			"nonce":     call.Nonce,
			"to":        call.To,
			"value":     call.Value.String(),
			"gas_limit": call.GasLimit,
			"gas_price": call.GasPrice.String(),
			"chain_id":  a.cfg.NetworkID,
			"data":      append([]byte(nil), call.Data...),
		},
	}
	transaction, signer, err := a.transactionFromUnsigned(unsigned)
	if err != nil {
		return nil, err
	}
	unsigned.RawBytes = signer.Hash(transaction).Bytes()
	return unsigned, nil
}
