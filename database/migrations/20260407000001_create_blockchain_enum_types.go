package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000001CreateBlockchainEnumTypes struct{}

func (r *M20260407000001CreateBlockchainEnumTypes) Signature() string {
	return "20260407000001_create_blockchain_enum_types"
}

func (r *M20260407000001CreateBlockchainEnumTypes) Up() error {
	enumTypes := []struct {
		name   string
		values string
	}{
		{"wallet_status", "'active', 'pending', 'archived', 'frozen'"},
		{"wallet_read_model_status", "'idle', 'syncing', 'synced', 'stale', 'failed'"},
		{"asset_type", "'native', 'token'"},
		{"wallet_sync_scope", "'balances', 'transactions', 'tokens', 'utxos', 'full'"},
		{"wallet_sync_status", "'idle', 'syncing', 'synced', 'stale', 'failed'"},
		{"utxo_status", "'unspent', 'locked', 'spent', 'orphaned'"},
		{"transaction_status", "'pending', 'confirming', 'confirmed', 'failed', 'dropped'"},
		{"transaction_type", "'deposit', 'withdrawal', 'transfer', 'token_transfer', 'fee'"},
		{"transaction_direction", "'inbound', 'outbound', 'self', 'unknown'"},
		{"transaction_source", "'chain', 'deposit_ingest', 'withdrawal_flow', 'reconciliation'"},
	}

	for _, e := range enumTypes {
		_, err := facades.Orm().Query().Exec(
			"DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = '" + e.name + "') THEN CREATE TYPE " + e.name + " AS ENUM (" + e.values + "); END IF; END $$",
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *M20260407000001CreateBlockchainEnumTypes) Down() error {
	dropOrder := []string{
		"transaction_source",
		"transaction_direction",
		"transaction_type",
		"transaction_status",
		"utxo_status",
		"wallet_sync_status",
		"wallet_sync_scope",
		"asset_type",
		"wallet_read_model_status",
		"wallet_status",
	}

	for _, name := range dropOrder {
		_, err := facades.Orm().Query().Exec("DROP TYPE IF EXISTS " + name)
		if err != nil {
			return err
		}
	}

	return nil
}
