package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000003CreateWalletBalanceSnapshotsTable struct{}

func (r *M20260407000003CreateWalletBalanceSnapshotsTable) Signature() string {
	return "20260407000003_create_wallet_balance_snapshots_table"
}

func (r *M20260407000003CreateWalletBalanceSnapshotsTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS wallet_balance_snapshots (
			id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			wallet_id       UUID        NOT NULL REFERENCES wallets(id),
			chain_id        VARCHAR(32) NOT NULL REFERENCES chains(id),
			balance_asset   VARCHAR(32) NOT NULL,
			balance_raw     TEXT        NOT NULL,
			balance_display TEXT        NOT NULL,
			balance_usd     NUMERIC(28, 10),
			captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_wallet_balance_snapshots_wallet_id
			ON wallet_balance_snapshots(wallet_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_balance_snapshots_chain_id
			ON wallet_balance_snapshots(chain_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_balance_snapshots_captured_at
			ON wallet_balance_snapshots(captured_at);
	`)
	return err
}

func (r *M20260407000003CreateWalletBalanceSnapshotsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_balance_snapshots`)
	return err
}
