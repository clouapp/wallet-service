package chainfixtures

import (
	evmchain "github.com/macrowallets/waas/app/adapters/chain/evm"
	"github.com/macrowallets/waas/app/models"
)

// EVMSepoliaConfirmations is the confirmation depth of the Sepolia fixture.
const EVMSepoliaConfirmations = 3

// NewEVMSepolia builds the production EVM adapter for teth (Ethereum Sepolia)
// against rpcURL, a fake node such as mocks.FakeEVMNode.
func NewEVMSepolia(rpcURL string) *evmchain.EVMLive {
	return evmchain.NewEVMLive(evmchain.EVMConfig{
		ChainIDStr: models.ChainTETH, ChainName: "Ethereum Sepolia", NativeSymbol: models.NativeETH, NativeDecimal: 18,
		NetworkID: models.EVMNetworkIDEthereumSepolia, RPCURL: rpcURL, Confirmations: EVMSepoliaConfirmations,
	})
}
