package chain

import "math/big"

// SolanaFeeQuote prices one transfer as the live client encodes it: one signature,
// plus the recipient's associated token account when it must be created.
// The live Solana client returns this shape; sweep keeps the port.
type SolanaFeeQuote struct {
	Signatures              int
	LamportsPerSignature    int64
	AccountCreationLamports *big.Int
}

// Fee is the lamports the transfer costs its fee payer besides the amount.
func (q SolanaFeeQuote) Fee() *big.Int {
	fee := new(big.Int).Mul(big.NewInt(q.LamportsPerSignature), big.NewInt(int64(q.Signatures)))
	if q.AccountCreationLamports != nil {
		fee.Add(fee, q.AccountCreationLamports)
	}
	return fee
}
