package chain

// Bounds FeePolicy applies to a Bitcoin fee rate. The live Bitcoin adapter
// keeps the same numbers when it turns a sat/vB estimate into milli-sat/vB.
const (
	milliSatsPerSat = 1000
	// btcMinRelayMilliSatPerVByte is the default min relay fee (1 sat/vB).
	btcMinRelayMilliSatPerVByte = 1 * milliSatsPerSat
	// btcMaxSaneSatPerVByte rejects an estimator answer as garbage above it.
	btcMaxSaneSatPerVByte = 10_000
)
