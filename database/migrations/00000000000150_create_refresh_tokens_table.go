package migrations

import "github.com/goravel/framework/facades"

type M00000000000150CreateRefreshTokensTable struct{}

func (r *M00000000000150CreateRefreshTokensTable) Signature() string {
	return "00000000000150_create_refresh_tokens_table"
}

func (r *M00000000000150CreateRefreshTokensTable) Up() error {
	stmts := []string{
		`CREATE TABLE refresh_tokens (
			id         UUID        NOT NULL,
			user_id    UUID        NOT NULL,
			token_hash TEXT        NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT refresh_tokens_pkey            PRIMARY KEY (id),
			CONSTRAINT refresh_tokens_user_id_foreign FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX refresh_tokens_user_id_index ON refresh_tokens (user_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000150CreateRefreshTokensTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS refresh_tokens`)
	return err
}
