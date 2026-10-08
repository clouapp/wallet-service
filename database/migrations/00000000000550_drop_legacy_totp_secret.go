package migrations

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"
)

// M00000000000550DropLegacyTotpSecret drops users.totp_secret and
// totp_recovery_codes after 510 copied them into mfa_credentials and
// mfa_backup_codes. A leftover value aborts the drop. Down adds the column
// and the table back empty and does not reconstruct secrets or recovery codes.
type M00000000000550DropLegacyTotpSecret struct{}

func (r *M00000000000550DropLegacyTotpSecret) Signature() string {
	return "00000000000550_drop_legacy_totp_secret"
}

func (r *M00000000000550DropLegacyTotpSecret) Up() error {
	return migrationTransaction(dropLegacyTotpSecret)
}

func (r *M00000000000550DropLegacyTotpSecret) Down() error {
	return migrationTransaction(restoreEmptyLegacyTotpSecret)
}

func dropLegacyTotpSecret(tx orm.Query) error {
	column, err := relationPresent(tx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'users'
		  AND column_name = 'totp_secret'`)
	if err != nil {
		return err
	}
	if column {
		left, err := countQuery(tx, `SELECT count(*) FROM users WHERE COALESCE(totp_secret, '') <> ''`)
		if err != nil {
			return err
		}
		if left > 0 {
			return fmt.Errorf("drop users.totp_secret: %d values are still stored", left)
		}
		if _, err := tx.Exec(`ALTER TABLE users DROP COLUMN totp_secret`); err != nil {
			return fmt.Errorf("drop users.totp_secret: %w", err)
		}
	}

	table, err := relationPresent(tx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'totp_recovery_codes'`)
	if err != nil {
		return err
	}
	if !table {
		return nil
	}
	left, err := countQuery(tx, `SELECT count(*) FROM totp_recovery_codes`)
	if err != nil {
		return err
	}
	if left > 0 {
		return fmt.Errorf("drop totp_recovery_codes: %d rows are still stored", left)
	}
	if _, err := tx.Exec(`DROP TABLE totp_recovery_codes`); err != nil {
		return fmt.Errorf("drop totp_recovery_codes: %w", err)
	}
	return nil
}

func restoreEmptyLegacyTotpSecret(tx orm.Query) error {
	statements := []string{
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT`,
		`CREATE TABLE IF NOT EXISTS totp_recovery_codes (
			id         UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			code_hash  TEXT        NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT totp_recovery_codes_pkey            PRIMARY KEY (id),
			CONSTRAINT totp_recovery_codes_user_id_foreign FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS totp_recovery_codes_user_id_index ON totp_recovery_codes (user_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("restore empty legacy totp store: %w", err)
		}
	}
	return nil
}
