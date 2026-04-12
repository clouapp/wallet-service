package refresh

import "fmt"

var evmChains = map[string]bool{
	"eth":      true,
	"teth":     true,
	"polygon":  true,
	"tpolygon": true,
}

var bitcoinChains = map[string]bool{
	"btc":  true,
	"tbtc": true,
}

var solanaChains = map[string]bool{
	"sol":  true,
	"tsol": true,
}

func ExpandScopes(req RefreshRequest) ([]RefreshScope, error) {
	if req.Scope != RefreshScopeFull {
		return []RefreshScope{req.Scope}, nil
	}

	switch {
	case bitcoinChains[req.ChainID]:
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeUtxos}, nil
	case solanaChains[req.ChainID]:
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
	case evmChains[req.ChainID]:
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
	default:
		return nil, fmt.Errorf("unknown chain ID %q for full scope expansion", req.ChainID)
	}
}
