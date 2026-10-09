package chains

import (
	chainresource "github.com/macrowallets/waas/app/http/resources/chains"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

// ChainDetail is the chain with its tokens and resources. The fields are in
// the order the JSON object has always been written (sorted by key). A list the
// service could not read is null.
type ChainDetail struct {
	Chain     *chainresource.Chain `json:"chain"`
	Resources []ChainResource      `json:"resources"`
	Tokens    []Token              `json:"tokens"`
}

// NewChainDetail projects a chain with its tokens and resources.
func NewChainDetail(detail chainsvc.Detail) ChainDetail {
	return ChainDetail{
		Chain:     chainresource.ChainPtr(detail.Chain),
		Resources: ChainResourcesFrom(detail.Resources),
		Tokens:    TokensFrom(detail.Tokens),
	}
}
