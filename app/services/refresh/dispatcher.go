package refresh

import (
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

var solanaChains = map[string]bool{
	"sol":  true,
	"tsol": true,
}

var tronChains = map[string]bool{
	models.ChainTron:  true,
	models.ChainTTron: true,
}

var xrpChains = map[string]bool{
	models.ChainXRP:  true,
	models.ChainTXRP: true,
}

func ExpandScopes(req RefreshRequest) ([]RefreshScope, error) {
	if req.Scope != RefreshScopeFull {
		return []RefreshScope{req.Scope}, nil
	}

	switch {
	case models.IsBitcoinFamilyChainID(req.ChainID):
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeUtxos}, nil
	case solanaChains[req.ChainID], tronChains[req.ChainID]:
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
	case xrpChains[req.ChainID]:
		return []RefreshScope{RefreshScopeBalances}, nil
	case models.IsEVMChainID(req.ChainID):
		return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
	default:
		return nil, fmt.Errorf("unknown chain ID %q for full scope expansion", req.ChainID)
	}
}
