package rpc

import "github.com/macrowallets/waas/app/services/chain"

func init() {
	chain.SetJSONRPCCaller(func(deps chain.JSONRPCDeps) chain.JSONRPCCaller {
		return NewRPCClient(RPCClientDeps{URL: deps.URL, User: deps.User, Password: deps.Password})
	})
}
