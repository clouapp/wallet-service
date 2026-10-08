package evm

import "github.com/macrowallets/waas/app/services/chain"

func init() {
	chain.SetEVMCallBuilder(func(chainID, chainName string, networkID int64) chain.EVMCallBuilder {
		return NewEVMLive(EVMConfig{ChainIDStr: chainID, ChainName: chainName, NetworkID: networkID})
	})
}
