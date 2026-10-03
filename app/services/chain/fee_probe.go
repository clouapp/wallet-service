package chain

// EVMFeeProbeRecipient is the recipient a fee quote uses when the real one is not
// known yet: the same probe the L1 data fee sizing uses.
func EVMFeeProbeRecipient() string {
	return evmFeeProbeAddress.Hex()
}
