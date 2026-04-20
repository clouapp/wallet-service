package migrations

import "github.com/goravel/framework/facades"

type M00000000000001CreateEnumTypes struct{}

func (r *M00000000000001CreateEnumTypes) Signature() string {
	return "00000000000001_create_enum_types"
}

func (r *M00000000000001CreateEnumTypes) Up() error {
	enums := []struct {
		name   string
		values string
	}{
		{"wallet_status", "'active', 'pending', 'archived', 'frozen'"},
		{"wallet_read_model_status", "'idle', 'syncing', 'synced', 'stale', 'failed'"},
		{"wallet_gas_status", "'unseeded', 'seeded', 'low'"},
		{"asset_type", "'native', 'token'"},
		{"wallet_sync_scope", "'balances', 'transactions', 'tokens', 'utxos', 'full'"},
		{"wallet_sync_status", "'idle', 'syncing', 'synced', 'stale', 'failed'"},
		{"utxo_status", "'unspent', 'locked', 'spent', 'orphaned'"},
		{"transaction_status", "'pending', 'confirming', 'confirmed', 'failed', 'dropped'"},
		{"transaction_type", "'deposit', 'withdrawal', 'transfer', 'token_transfer', 'fee', 'sweep', 'gas_seed'"},
		{"transaction_direction", "'inbound', 'outbound', 'self', 'unknown'"},
		{"transaction_source", "'chain', 'deposit_ingest', 'withdrawal_flow', 'reconciliation'"},
		{"currency_type", "'crypto', 'fiat'"},
	}

	for _, e := range enums {
		stmt := "DO $$ BEGIN CREATE TYPE " + e.name + " AS ENUM (" + e.values + "); EXCEPTION WHEN duplicate_object THEN null; END $$"
		if _, err := facades.Orm().Query().Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000001CreateEnumTypes) Down() error {
	dropOrder := []string{
		"currency_type",
		"transaction_source",
		"transaction_direction",
		"transaction_type",
		"transaction_status",
		"utxo_status",
		"wallet_sync_status",
		"wallet_sync_scope",
		"asset_type",
		"wallet_gas_status",
		"wallet_read_model_status",
		"wallet_status",
	}
	for _, name := range dropOrder {
		if _, err := facades.Orm().Query().Exec("DROP TYPE IF EXISTS " + name); err != nil {
			return err
		}
	}
	return nil
}
