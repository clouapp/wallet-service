package chain

import "math/big"

// BitcoinFeeQuote prices one transfer. Covered is false when the inputs cannot pay
// the amount; the quote is then for a typical one-input transfer with change.
// The live Bitcoin client returns this shape; sweep keeps the port.
type BitcoinFeeQuote struct {
	Fee              int64
	Inputs           int
	Outputs          int
	VSize            int64
	MilliSatPerVByte int64
	FlatFallback     bool
	Covered          bool
}

// SpendableFunds is what an address can put toward a native transfer. Balance is
// what the transaction builder will spend (Bitcoin: confirmed UTXOs only);
// MaxTransferFee is the fee of one transfer spending all of it to a single
// recipient, so Balance − MaxTransferFee is the most one transfer can pay. When that
// is below dust, MaxTransferFee is the whole Balance: nothing can be sent.
// The live Bitcoin client returns this shape; sweep keeps the port.
type SpendableFunds struct {
	Balance        *big.Int
	MaxTransferFee *big.Int
}
