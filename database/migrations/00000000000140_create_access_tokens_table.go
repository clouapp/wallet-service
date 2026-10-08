package migrations

import "github.com/goravel/framework/facades"

type M00000000000140CreateAccessTokensTable struct{}

func (r *M00000000000140CreateAccessTokensTable) Signature() string {
	return "00000000000140_create_access_tokens_table"
}

func (r *M00000000000140CreateAccessTokensTable) Up() error {
	stmts := []string{
		`CREATE TABLE access_tokens (
			id              UUID         NOT NULL,
			account_id      UUID         NOT NULL,
			created_by      UUID,
			name            VARCHAR(255) NOT NULL,
			token_hash      TEXT         NOT NULL,
			permissions     TEXT,
			ip_cidr         TEXT,
			spending_limit  JSON,
			valid_until     TIMESTAMPTZ,
			created_at      TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at      TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT access_tokens_pkey               PRIMARY KEY (id),
			CONSTRAINT access_tokens_account_id_foreign FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX access_tokens_account_id_index ON access_tokens (account_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000140CreateAccessTokensTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS access_tokens`)
	return err
}
