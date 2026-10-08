package migrations

import "github.com/goravel/framework/facades"

type M00000000000220CreateWalletAssetBalancesTable struct{}

func (r *M00000000000220CreateWalletAssetBalancesTable) Signature() string {
	return "00000000000220_create_wallet_asset_balances_table"
}

func (r *M00000000000220CreateWalletAssetBalancesTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallet_asset_balances (
			id              UUID         NOT NULL DEFAULT gen_random_uuid(),
			wallet_id       UUID         NOT NULL,
			chain_id        VARCHAR(32)  NOT NULL,
			asset_type      asset_type   NOT NULL,
			asset_symbol    VARCHAR(32)  NOT NULL,
			asset_name      VARCHAR(128),
			asset_contract  VARCHAR(255),
			asset_key       VARCHAR(320) NOT NULL,
			decimals        INTEGER      NOT NULL,
			amount_raw      TEXT         NOT NULL,
			amount_display  TEXT         NOT NULL,
			price_usd       NUMERIC(28, 10),
			value_usd       NUMERIC(28, 10),
			source_address  TEXT,
			last_synced_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			CONSTRAINT wallet_asset_balances_pkey                            PRIMARY KEY (id),
			CONSTRAINT wallet_asset_balances_wallet_id_chain_id_asset_key_key UNIQUE (wallet_id, chain_id, asset_key),
			CONSTRAINT wallet_asset_balances_wallet_id_fkey                  FOREIGN KEY (wallet_id) REFERENCES wallets(id),
			CONSTRAINT wallet_asset_balances_chain_id_fkey                   FOREIGN KEY (chain_id)  REFERENCES chains(id)
		)`,
		`CREATE INDEX idx_wallet_asset_balances_wallet_id ON wallet_asset_balances (wallet_id)`,
		`CREATE INDEX idx_wallet_asset_balances_chain_id  ON wallet_asset_balances (chain_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000220CreateWalletAssetBalancesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_asset_balances`)
	return err
}
