package migrations

import "github.com/goravel/framework/facades"

type M00000000000030CreateUsersTable struct{}

func (r *M00000000000030CreateUsersTable) Signature() string {
	return "00000000000030_create_users_table"
}

func (r *M00000000000030CreateUsersTable) Up() error {
	stmts := []string{
		`CREATE TABLE users (
			id                 UUID         NOT NULL,
			email              VARCHAR(255) NOT NULL,
			password_hash      TEXT         NOT NULL,
			full_name          VARCHAR(255),
			totp_secret        TEXT,
			totp_enabled       BOOLEAN      NOT NULL DEFAULT FALSE,
			status             VARCHAR(20)  NOT NULL DEFAULT 'active',
			default_account_id UUID,
			created_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			preferences        JSONB        NOT NULL DEFAULT '{}'::jsonb,
			CONSTRAINT users_pkey PRIMARY KEY (id),
			CONSTRAINT users_email_unique UNIQUE (email),
			CONSTRAINT users_default_account_id_foreign FOREIGN KEY (default_account_id) REFERENCES accounts(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX users_email_index ON users (email)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000030CreateUsersTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS users`)
	return err
}
