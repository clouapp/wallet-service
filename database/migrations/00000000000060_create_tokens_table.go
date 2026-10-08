package migrations

import "github.com/goravel/framework/facades"

type M00000000000060CreateTokensTable struct{}

func (r *M00000000000060CreateTokensTable) Signature() string {
	return "00000000000060_create_tokens_table"
}

func (r *M00000000000060CreateTokensTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE tokens (
			id               UUID         NOT NULL,
			chain_id         VARCHAR(20)  NOT NULL,
			symbol           VARCHAR(20)  NOT NULL,
			name             VARCHAR(100) NOT NULL,
			contract_address VARCHAR(255) NOT NULL,
			decimals         INTEGER      NOT NULL,
			icon_url         VARCHAR(500),
			status           VARCHAR(20)  NOT NULL DEFAULT 'active',
			created_at       TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at       TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT tokens_pkey PRIMARY KEY (id),
			CONSTRAINT tokens_chain_id_foreign FOREIGN KEY (chain_id) REFERENCES chains(id) ON DELETE CASCADE
		)
	`)
	return err
}

func (r *M00000000000060CreateTokensTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS tokens`)
	return err
}
