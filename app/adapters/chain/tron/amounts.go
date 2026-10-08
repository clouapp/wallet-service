package tron

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

const percentDenominator = 100

func fmtUnits(units *big.Int, decimals uint8) string {
	if units == nil {
		return "0"
	}
	return amount.FormatBaseUnits(units, int(decimals))
}

func validateTokenTransferForEstimate(req types.TransferRequest) error {
	switch {
	case req.From == "":
		return fmt.Errorf("%w: %s transfer has no sender", chain.ErrGasEstimateFailed, req.Token.Symbol)
	case req.To == "":
		return fmt.Errorf("%w: %s transfer has no recipient", chain.ErrGasEstimateFailed, req.Token.Symbol)
	case req.Amount == nil || req.Amount.Sign() <= 0:
		return fmt.Errorf("%w: %s transfer has no positive amount", chain.ErrGasEstimateFailed, req.Token.Symbol)
	}
	return nil
}

func normalizeEVMCompressedPublicKey(publicKey []byte) ([]byte, error) {
	if len(publicKey) == 33 {
		if _, err := crypto.DecompressPubkey(publicKey); err != nil {
			return nil, fmt.Errorf("invalid compressed EVM public key: %w", err)
		}
		return append([]byte(nil), publicKey...), nil
	}
	parsed, err := crypto.UnmarshalPubkey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid EVM public key: %w", err)
	}
	return crypto.CompressPubkey(parsed), nil
}

func recoverEVMRecoveryID(hash, signature, expectedCompressedPublicKey []byte) ([]byte, error) {
	for recoveryID := byte(0); recoveryID <= 1; recoveryID++ {
		candidate := make([]byte, 65)
		copy(candidate, signature)
		candidate[64] = recoveryID
		recovered, err := crypto.SigToPub(hash, candidate)
		if err != nil {
			continue
		}
		if bytes.Equal(crypto.CompressPubkey(recovered), expectedCompressedPublicKey) {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("MPC signature does not match wallet public key")
}
