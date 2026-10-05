package chain

// evmFeeProbeRecipient is the recipient a fee quote uses when the real one is not
// known yet. The live EVM adapter sizes L1 data fees with the same address.
const evmFeeProbeRecipient = "0x1111111111111111111111111111111111111111"

// EVMFeeProbeRecipient is the recipient a fee quote uses when the real one is not
// known yet: the same probe the L1 data fee sizing uses.
func EVMFeeProbeRecipient() string {
	return evmFeeProbeRecipient
}
