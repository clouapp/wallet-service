package chain

import "math/big"

// EVMGasSeedBufferPercent is the headroom the EVM sweep builder funds above
// the child's estimated token-transfer fee. Planning and execution share it.
const EVMGasSeedBufferPercent int64 = 120

// EVMGasSeedAmount matches the native amount BuildSweep sends to a child.
func EVMGasSeedAmount(fee *big.Int) *big.Int {
	return new(big.Int).Div(new(big.Int).Mul(fee, big.NewInt(EVMGasSeedBufferPercent)), big.NewInt(100))
}
