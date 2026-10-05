package chain

import (
	"math/big"

	"github.com/macrowallets/waas/pkg/types"
)

// EVMCall is a fully specified legacy EVM transaction (contract call or plain
// transfer). Nothing in it is fetched from a node: the caller simulated it and
// chose nonce, gas limit and gas price.
type EVMCall struct {
	Nonce    uint64
	To       string
	Value    *big.Int
	Data     []byte
	GasLimit uint64
	GasPrice *big.Int
}

// EVMCallBuilder is the live client evmcall uses to encode one call. The chain
// service keeps this port; the EVM adapter registers the client.
type EVMCallBuilder interface {
	types.Chain
	BuildCall(call EVMCall) (*types.UnsignedTx, error)
}

var newEVMCallBuilder func(chainID, chainName string, networkID int64) EVMCallBuilder

// SetEVMCallBuilder registers the live client. The EVM adapter calls it.
func SetEVMCallBuilder(build func(chainID, chainName string, networkID int64) EVMCallBuilder) {
	newEVMCallBuilder = build
}

// NewEVMCallBuilder returns the registered live client, or nil when that
// adapter is not linked into the process.
func NewEVMCallBuilder(chainID, chainName string, networkID int64) EVMCallBuilder {
	if newEVMCallBuilder == nil {
		return nil
	}
	return newEVMCallBuilder(chainID, chainName, networkID)
}
