package migrations

import "github.com/goravel/framework/facades"

type M00000000000240CreateWalletSyncStatesTable struct{}

func (r *M00000000000240CreateWalletSyncStatesTable) Signature() string {
	return "00000000000240_create_wallet_sync_states_table"
}

func (r *M00000000000240CreateWalletSyncStatesTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallet_sync_states (
			id                UUID               NOT NULL DEFAULT gen_random_uuid(),
			wallet_id         UUID               NOT NULL,
			chain_id          VARCHAR(32)        NOT NULL,
			sync_scope        wallet_sync_scope  NOT NULL,
			status            wallet_sync_status NOT NULL,
			cursor            TEXT,
			cursor_meta       JSONB,
			last_synced_at    TIMESTAMPTZ,
			last_attempted_at TIMESTAMPTZ,
			last_error        TEXT,
			next_reconcile_at TIMESTAMPTZ,
			created_at        TIMESTAMPTZ        NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ        NOT NULL DEFAULT NOW(),
			CONSTRAINT wallet_sync_states_pkey                              PRIMARY KEY (id),
			CONSTRAINT wallet_sync_states_wallet_id_chain_id_sync_scope_key UNIQUE (wallet_id, chain_id, sync_scope),
			CONSTRAINT wallet_sync_states_wallet_id_fkey                    FOREIGN KEY (wallet_id) REFERENCES wallets(id),
			CONSTRAINT wallet_sync_states_chain_id_fkey                     FOREIGN KEY (chain_id)  REFERENCES chains(id)
		)`,
		`CREATE INDEX idx_wallet_sync_states_wallet_id       ON wallet_sync_states (wallet_id)`,
		`CREATE INDEX idx_wallet_sync_states_status          ON wallet_sync_states (status)`,
		`CREATE INDEX idx_wallet_sync_states_next_reconcile  ON wallet_sync_states (next_reconcile_at)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000240CreateWalletSyncStatesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_sync_states`)
	return err
}
