package migrations

import "github.com/goravel/framework/facades"

type M00000000000170CreateTotpRecoveryCodesTable struct{}

func (r *M00000000000170CreateTotpRecoveryCodesTable) Signature() string {
	return "00000000000170_create_totp_recovery_codes_table"
}

func (r *M00000000000170CreateTotpRecoveryCodesTable) Up() error {
	stmts := []string{
		`CREATE TABLE totp_recovery_codes (
			id         UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			code_hash  TEXT        NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT totp_recovery_codes_pkey            PRIMARY KEY (id),
			CONSTRAINT totp_recovery_codes_user_id_foreign FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX totp_recovery_codes_user_id_index ON totp_recovery_codes (user_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000170CreateTotpRecoveryCodesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS totp_recovery_codes`)
	return err
}
