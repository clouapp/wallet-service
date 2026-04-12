package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000004CreateWalletAssetBalancesTable struct{}

func (r *M20260407000004CreateWalletAssetBalancesTable) Signature() string {
	return "20260407000004_create_wallet_asset_balances_table"
}

func (r *M20260407000004CreateWalletAssetBalancesTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS wallet_asset_balances (
			id              UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
			wallet_id       UUID          NOT NULL REFERENCES wallets(id),
			chain_id        VARCHAR(32)   NOT NULL REFERENCES chains(id),
			asset_type      asset_type    NOT NULL,
			asset_symbol    VARCHAR(32)   NOT NULL,
			asset_name      VARCHAR(128),
			asset_contract  VARCHAR(255),
			asset_key       VARCHAR(320)  NOT NULL,
			decimals        INT           NOT NULL,
			amount_raw      TEXT          NOT NULL,
			amount_display  TEXT          NOT NULL,
			price_usd       NUMERIC(28, 10),
			value_usd       NUMERIC(28, 10),
			source_address  TEXT,
			last_synced_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

			UNIQUE(wallet_id, chain_id, asset_key)
		);

		CREATE INDEX IF NOT EXISTS idx_wallet_asset_balances_wallet_id
			ON wallet_asset_balances(wallet_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_asset_balances_chain_id
			ON wallet_asset_balances(chain_id);
	`)
	return err
}

func (r *M20260407000004CreateWalletAssetBalancesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_asset_balances`)
	return err
}
