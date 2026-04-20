package migrations

import "github.com/goravel/framework/facades"

type M00000000000160CreatePasswordResetTokensTable struct{}

func (r *M00000000000160CreatePasswordResetTokensTable) Signature() string {
	return "00000000000160_create_password_reset_tokens_table"
}

func (r *M00000000000160CreatePasswordResetTokensTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE password_reset_tokens (
			id         UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			token_hash TEXT        NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT password_reset_tokens_pkey             PRIMARY KEY (id),
			CONSTRAINT password_reset_tokens_user_id_foreign  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)
	`)
	return err
}

func (r *M00000000000160CreatePasswordResetTokensTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS password_reset_tokens`)
	return err
}
