package refresh

type RefreshScope string

const (
	RefreshScopeBalances     RefreshScope = "balances"
	RefreshScopeTransactions RefreshScope = "transactions"
	RefreshScopeTokens       RefreshScope = "tokens"
	RefreshScopeUtxos        RefreshScope = "utxos"
	RefreshScopeFull         RefreshScope = "full"
)

type RefreshRequest struct {
	ChainID   string
	WalletID  string
	Addresses []string
	Currency  string
	TxHash    string
	Scope     RefreshScope
	Reason    string
	Force     bool
}
