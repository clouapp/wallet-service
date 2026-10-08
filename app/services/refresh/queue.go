package refresh

// Dispatcher enqueues one wallet refresh or reconcile on the blockchain queue
// and fires the domain events whose only job is that enqueue. The payload is
// the wallet id, the chain id, and for a deposit the transaction hash.
// Implementations must not put a credential or a secret on the queue.
type Dispatcher interface {
	DispatchBalances(walletID, chainID string) error
	DispatchTransactions(walletID, chainID string) error
	DispatchTokens(walletID, chainID string) error
	DispatchUTXOs(walletID, chainID string) error
	DispatchReconcile(walletID, chainID string) error
	DispatchWalletCreated(walletID, chainID string) error
	DispatchWalletActivated(walletID, chainID string) error
	DispatchDepositDetected(walletID, chainID, txHash string) error
	DispatchWithdrawalBroadcasted(walletID, chainID string) error
}
