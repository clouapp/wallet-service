package migrations

import "github.com/goravel/framework/facades"

// M00000000000290CreateSettingsTable stores one value per (account, group, key).
// Metadata lives in the settings registry. Platform rows (account_id NULL)
// share the table so a later platform audience does not need a second shape;
// this migration does not seed them.
type M00000000000290CreateSettingsTable struct{}

func (r *M00000000000290CreateSettingsTable) Signature() string {
	return "00000000000290_create_settings_table"
}

func (r *M00000000000290CreateSettingsTable) Up() error {
	stmts := []string{
		`CREATE TABLE settings (
			id         BIGSERIAL    NOT NULL,
			account_id UUID,
			"group"    TEXT         NOT NULL,
			"key"      TEXT         NOT NULL,
			value      TEXT,
			created_at TIMESTAMP(6) WITH TIME ZONE,
			updated_at TIMESTAMP(6) WITH TIME ZONE,
			CONSTRAINT settings_pkey PRIMARY KEY (id),
			CONSTRAINT settings_account_id_foreign FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX settings_account_entry_idx ON settings (account_id, "group", "key") WHERE account_id IS NOT NULL`,
		`CREATE UNIQUE INDEX settings_platform_entry_idx ON settings ("group", "key") WHERE account_id IS NULL`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000290CreateSettingsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS settings`)
	return err
}
