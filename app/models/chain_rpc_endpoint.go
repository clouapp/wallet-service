package models

import "time"

// ChainRPCPrimaryPosition is the place of chains.rpc_url in a chain's endpoint
// list. A ChainRPCEndpoint row is a fallback at a later position.
const ChainRPCPrimaryPosition = 1

// ChainRPCEndpoint is one fallback RPC endpoint of a chain: tried, in Position
// order, when the endpoints before it cannot serve a call. URL is sealed like
// chains.rpc_url and is never logged or returned. A disabled row stays in the
// list and is not called.
type ChainRPCEndpoint struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	ChainID   string    `gorm:"column:chain_id"`
	URL       string    `gorm:"column:url"`
	Position  int       `gorm:"column:position"`
	Enabled   bool      `gorm:"column:enabled"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (ChainRPCEndpoint) TableName() string { return "chain_rpc_endpoints" }

// RPCFallbacksSupported reports whether chains of adapterType fail over through
// chain_rpc_endpoints: EVM and Solana, whose adapters share the JSON-RPC client.
// The Bitcoin family keeps its own fallback list (vault.utxo_fallbacks); TRON and
// XRP have none yet.
func RPCFallbacksSupported(adapterType string) bool {
	return adapterType == AdapterTypeEVM || adapterType == AdapterTypeSolana
}
