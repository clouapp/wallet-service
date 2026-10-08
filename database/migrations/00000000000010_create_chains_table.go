package migrations

import "github.com/goravel/framework/facades"

type M00000000000010CreateChainsTable struct{}

func (r *M00000000000010CreateChainsTable) Signature() string {
	return "00000000000010_create_chains_table"
}

func (r *M00000000000010CreateChainsTable) Up() error {
	stmts := []string{
		`CREATE TABLE chains (
			id                          VARCHAR(20)    NOT NULL,
			name                        VARCHAR(100)   NOT NULL,
			adapter_type                VARCHAR(20)    NOT NULL,
			native_symbol               VARCHAR(20)    NOT NULL,
			native_decimals             INTEGER        NOT NULL,
			network_id                  BIGINT,
			rpc_url                     TEXT           NOT NULL,
			is_testnet                  BOOLEAN        NOT NULL DEFAULT FALSE,
			mainnet_chain_id            VARCHAR(20),
			required_confirmations      INTEGER        NOT NULL,
			icon_url                    VARCHAR(500),
			display_order               INTEGER        NOT NULL DEFAULT 0,
			status                      VARCHAR(20)    NOT NULL DEFAULT 'active',
			created_at                  TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at                  TIMESTAMP(0) WITHOUT TIME ZONE,
			gas_readiness_threshold_raw TEXT,
			dust_threshold_native_raw   TEXT,
			dust_threshold_usd          NUMERIC(16, 4),
			CONSTRAINT chains_pkey PRIMARY KEY (id),
			CONSTRAINT chains_mainnet_chain_id_foreign FOREIGN KEY (mainnet_chain_id) REFERENCES chains(id) ON DELETE SET NULL
		)`,
		`COMMENT ON COLUMN chains.gas_readiness_threshold_raw IS 'Min native balance (raw units) on BaseAddress to consider wallet gas-ready'`,
		`COMMENT ON COLUMN chains.dust_threshold_native_raw   IS 'Min native balance on a child to be considered sweepable (raw units)'`,
		`COMMENT ON COLUMN chains.dust_threshold_usd          IS 'Min USD-equivalent for token dust filter'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000010CreateChainsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS chains`)
	return err
}
