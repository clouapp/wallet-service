package migrations

// M00000000000650CreateChainRPCEndpointsTable stores the fallback RPC endpoints of
// a chain. Position 1 of a chain's list is chains.rpc_url (the primary, unchanged);
// the rows here are positions 2 and after, tried in that order when the endpoints
// before them cannot serve a call. A URL is sealed like chains.rpc_url (enc:v1:,
// APP_KEY): the CHECK refuses a plaintext row. A disabled row is kept in the list
// but never called. The rows go with their chain.
type M00000000000650CreateChainRPCEndpointsTable struct{}

func (r *M00000000000650CreateChainRPCEndpointsTable) Signature() string {
	return "00000000000650_create_chain_rpc_endpoints_table"
}

func (r *M00000000000650CreateChainRPCEndpointsTable) Up() error {
	return execMigrationSQL(`
		CREATE TABLE IF NOT EXISTS chain_rpc_endpoints (
			id BIGSERIAL PRIMARY KEY,
			chain_id VARCHAR(20) NOT NULL REFERENCES chains(id) ON DELETE CASCADE,
			url TEXT NOT NULL,
			position INTEGER NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chain_rpc_endpoints_chain_position_unique UNIQUE (chain_id, position),
			CONSTRAINT chain_rpc_endpoints_after_primary CHECK (position >= 2),
			CONSTRAINT chain_rpc_endpoints_url_sealed CHECK (url LIKE 'enc:v1:%')
		);
	`)
}

func (r *M00000000000650CreateChainRPCEndpointsTable) Down() error {
	return execMigrationSQL(`DROP TABLE IF EXISTS chain_rpc_endpoints`)
}
