package migrations

import "github.com/goravel/framework/facades"

type M00000000000200CreateWhitelistEntriesTable struct{}

func (r *M00000000000200CreateWhitelistEntriesTable) Signature() string {
	return "00000000000200_create_whitelist_entries_table"
}

func (r *M00000000000200CreateWhitelistEntriesTable) Up() error {
	stmts := []string{
		`CREATE TABLE whitelist_entries (
			id         UUID         NOT NULL,
			wallet_id  UUID         NOT NULL,
			label      VARCHAR(255),
			address    TEXT         NOT NULL,
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT whitelist_entries_pkey              PRIMARY KEY (id),
			CONSTRAINT whitelist_entries_wallet_id_foreign FOREIGN KEY (wallet_id) REFERENCES wallets(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX whitelist_entries_wallet_id_index ON whitelist_entries (wallet_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000200CreateWhitelistEntriesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS whitelist_entries`)
	return err
}
