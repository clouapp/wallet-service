package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000007CreateWalletSyncStatesTable struct{}

func (r *M20260407000007CreateWalletSyncStatesTable) Signature() string {
	return "20260407000007_create_wallet_sync_states_table"
}

func (r *M20260407000007CreateWalletSyncStatesTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS wallet_sync_states (
			id                UUID               PRIMARY KEY DEFAULT gen_random_uuid(),
			wallet_id         UUID               NOT NULL REFERENCES wallets(id),
			chain_id          VARCHAR(32)        NOT NULL REFERENCES chains(id),
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

			UNIQUE(wallet_id, chain_id, sync_scope)
		);

		CREATE INDEX IF NOT EXISTS idx_wallet_sync_states_wallet_id
			ON wallet_sync_states(wallet_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_sync_states_status
			ON wallet_sync_states(status);
		CREATE INDEX IF NOT EXISTS idx_wallet_sync_states_next_reconcile
			ON wallet_sync_states(next_reconcile_at);
	`)
	return err
}

func (r *M20260407000007CreateWalletSyncStatesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_sync_states`)
	return err
}
