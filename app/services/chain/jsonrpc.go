package chain

import "context"

// JSONRPCCaller is the dial service callers used to construct as RPCClient.
// The rpc adapter registers the live client. Empty User and Password send no
// basic auth. The URL is not logged.
type JSONRPCCaller interface {
	Call(ctx context.Context, method string, out interface{}, params ...interface{}) error
}

// JSONRPCDeps is the endpoint and optional basic-auth pair the registered
// client stores.
type JSONRPCDeps struct {
	URL      string
	User     string
	Password string
}

var newJSONRPCCaller func(JSONRPCDeps) JSONRPCCaller

// SetJSONRPCCaller registers the live client. The rpc adapter calls it.
func SetJSONRPCCaller(dial func(JSONRPCDeps) JSONRPCCaller) {
	newJSONRPCCaller = dial
}

// NewJSONRPCCaller returns the registered client, or nil when that adapter is
// not linked into the process.
func NewJSONRPCCaller(deps JSONRPCDeps) JSONRPCCaller {
	if newJSONRPCCaller == nil {
		return nil
	}
	return newJSONRPCCaller(deps)
}
