package chain

import "math/big"

// tronFeeProbeRecipient (41cb0600…, the last 20 bytes of sha256("macro-wallets tron
// fee probe")) prices transfers whose recipient is not known yet. It is treated as
// never activated and holds no token, so a quote is the worst case: TRX pays the
// account activation, TRC-20 the first-time storage of the recipient's balance.
const tronFeeProbeRecipient = "TUUhMwaBT4s7Bd5AmAYUh4pJFQMViufXRL"

// TronFeeProbeRecipient is the recipient a fee quote uses when the real one is not
// known yet.
func TronFeeProbeRecipient() string { return tronFeeProbeRecipient }

// TronFeeQuote prices one transfer as the live Tron client's BuildTransfer encodes
// it, in sun. Every resource is paid by burning TRX: no free or staked bandwidth/energy is assumed.
//   - Bandwidth: (signed size + 64) bytes × getTransactionFee, except for a TRX
//     transfer that activates its recipient, which java-tron bills instead as
//     getCreateNewAccountFeeInSystemContract + getCreateAccountFee (ActivationFee).
//   - Energy (TRC-20): FeeLimit = simulated energy × getEnergyFee × 120 %, the
//     fee_limit encoded in the transaction and the most its call can burn.
type TronFeeQuote struct {
	BandwidthBytes      int64
	SunPerBandwidthByte int64
	BandwidthFee        *big.Int
	ActivationFee       *big.Int
	Energy              int64
	SunPerEnergy        int64
	FeeLimit            *big.Int
	// EnergyIsReference: the sender holds none of the token, so Energy is
	// tronTRC20ReferenceEnergy rather than a simulation.
	EnergyIsReference bool
}

// Fee is the most the transfer can cost its sender besides the amount.
func (q TronFeeQuote) Fee() *big.Int {
	fee := new(big.Int)
	for _, part := range []*big.Int{q.BandwidthFee, q.ActivationFee, q.FeeLimit} {
		if part != nil {
			fee.Add(fee, part)
		}
	}
	return fee
}
