package refresh

// Dispatcher enqueues one wallet refresh or reconcile on the blockchain queue.
// The payload is the wallet id and the chain id.
// Implementations must not put a credential or a secret on the queue.
type Dispatcher interface {
	DispatchBalances(walletID, chainID string) error
	DispatchTransactions(walletID, chainID string) error
	DispatchTokens(walletID, chainID string) error
	DispatchUTXOs(walletID, chainID string) error
	DispatchReconcile(walletID, chainID string) error
}
