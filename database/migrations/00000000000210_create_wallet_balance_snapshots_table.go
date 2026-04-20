package migrations

import "github.com/goravel/framework/facades"

type M00000000000210CreateWalletBalanceSnapshotsTable struct{}

func (r *M00000000000210CreateWalletBalanceSnapshotsTable) Signature() string {
	return "00000000000210_create_wallet_balance_snapshots_table"
}

func (r *M00000000000210CreateWalletBalanceSnapshotsTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallet_balance_snapshots (
			id              UUID        NOT NULL DEFAULT gen_random_uuid(),
			wallet_id       UUID        NOT NULL,
			chain_id        VARCHAR(32) NOT NULL,
			balance_asset   VARCHAR(32) NOT NULL,
			balance_raw     TEXT        NOT NULL,
			balance_display TEXT        NOT NULL,
			balance_usd     NUMERIC(28, 10),
			captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT wallet_balance_snapshots_pkey           PRIMARY KEY (id),
			CONSTRAINT wallet_balance_snapshots_wallet_id_fkey FOREIGN KEY (wallet_id) REFERENCES wallets(id),
			CONSTRAINT wallet_balance_snapshots_chain_id_fkey  FOREIGN KEY (chain_id)  REFERENCES chains(id)
		)`,
		`CREATE INDEX idx_wallet_balance_snapshots_wallet_id   ON wallet_balance_snapshots (wallet_id)`,
		`CREATE INDEX idx_wallet_balance_snapshots_chain_id    ON wallet_balance_snapshots (chain_id)`,
		`CREATE INDEX idx_wallet_balance_snapshots_captured_at ON wallet_balance_snapshots (captured_at)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000210CreateWalletBalanceSnapshotsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_balance_snapshots`)
	return err
}
