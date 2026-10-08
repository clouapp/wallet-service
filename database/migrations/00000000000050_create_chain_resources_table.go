package migrations

import "github.com/goravel/framework/facades"

type M00000000000050CreateChainResourcesTable struct{}

func (r *M00000000000050CreateChainResourcesTable) Signature() string {
	return "00000000000050_create_chain_resources_table"
}

func (r *M00000000000050CreateChainResourcesTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE chain_resources (
			id            UUID         NOT NULL,
			chain_id      VARCHAR(20)  NOT NULL,
			type          VARCHAR(20)  NOT NULL,
			name          VARCHAR(100) NOT NULL,
			url           VARCHAR(500) NOT NULL,
			description   TEXT,
			display_order INTEGER      NOT NULL DEFAULT 0,
			status        VARCHAR(20)  NOT NULL DEFAULT 'active',
			created_at    TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at    TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT chain_resources_pkey PRIMARY KEY (id),
			CONSTRAINT chain_resources_chain_id_foreign FOREIGN KEY (chain_id) REFERENCES chains(id) ON DELETE CASCADE
		)
	`)
	return err
}

func (r *M00000000000050CreateChainResourcesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS chain_resources`)
	return err
}
