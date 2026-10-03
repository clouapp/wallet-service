package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/pkg/types"
)

// chainRegistry is the chain lookup this rule uses to validate an address.
type chainRegistry interface {
	Chain(chainID string) (types.Chain, error)
}

type BlockchainAddress struct {
	registry chainRegistry
}

// NewBlockchainAddress validates addresses with the chain registry.
func NewBlockchainAddress(registry chainRegistry) *BlockchainAddress {
	if registry == nil {
		panic("blockchain address rule: chain registry is required")
	}
	return &BlockchainAddress{registry: registry}
}

func (r *BlockchainAddress) Signature() string {
	return "blockchain_address"
}

func (r *BlockchainAddress) Passes(_ context.Context, data validation.Data, val any, _ ...any) bool {
	addr, ok := val.(string)
	if !ok || addr == "" {
		return true
	}

	chainRaw, exists := data.Get("_chain")
	if !exists {
		return true
	}
	chainID, ok := chainRaw.(string)
	if !ok || chainID == "" {
		return true
	}

	adapter, err := r.registry.Chain(chainID)
	if err != nil || adapter == nil {
		return true
	}

	return adapter.ValidateAddress(addr)
}

func (r *BlockchainAddress) Message(_ context.Context) string {
	return "The :attribute is not a valid blockchain address for this chain."
}
