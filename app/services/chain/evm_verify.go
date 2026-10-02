package chain

import (
	"bytes"
	"fmt"
	"strings"

	gethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/pkg/types"
)

// VerifySignedTransaction decodes the signed EVM transaction, requires it to be the
// transaction that was built (same signing hash) and recovers its sender, which must
// be `from`. It runs after signing and before broadcast, independently of how the
// signature was produced.
func (a *EVMLive) VerifySignedTransaction(unsigned *types.UnsignedTx, signed *types.SignedTx, from string) error {
	if unsigned == nil || signed == nil || len(signed.RawBytes) == 0 {
		return fmt.Errorf("evm verify: unsigned and signed transactions are required")
	}
	if from == "" {
		return fmt.Errorf("evm verify: source address is required")
	}
	built, signer, err := a.transactionFromUnsigned(unsigned)
	if err != nil {
		return fmt.Errorf("evm verify: %w", err)
	}
	var decoded gethtypes.Transaction
	if err := decoded.UnmarshalBinary(signed.RawBytes); err != nil {
		return fmt.Errorf("evm verify: decode signed transaction: %w", err)
	}
	if !bytes.Equal(signer.Hash(&decoded).Bytes(), signer.Hash(built).Bytes()) {
		return fmt.Errorf("evm verify: signed transaction differs from the built one")
	}
	sender, err := gethtypes.Sender(signer, &decoded)
	if err != nil {
		return fmt.Errorf("evm verify: recover sender: %w", err)
	}
	if !strings.EqualFold(sender.Hex(), from) {
		return fmt.Errorf("evm verify: signed by %s, expected %s", sender.Hex(), from)
	}
	return nil
}
