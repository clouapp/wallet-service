package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000006CreateWalletUtxosTable struct{}

func (r *M20260407000006CreateWalletUtxosTable) Signature() string {
	return "20260407000006_create_wallet_utxos_table"
}

func (r *M20260407000006CreateWalletUtxosTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TABLE IF NOT EXISTS wallet_utxos (
			id               UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
			wallet_id        UUID          NOT NULL REFERENCES wallets(id),
			address_id       UUID          REFERENCES addresses(id),
			chain_id         VARCHAR(32)   NOT NULL REFERENCES chains(id),
			tx_hash          VARCHAR(255)  NOT NULL,
			output_index     INT           NOT NULL,
			address          TEXT          NOT NULL,
			value_raw        TEXT          NOT NULL,
			script_pub_key   TEXT,
			status           utxo_status   NOT NULL,
			spent_by_tx_hash VARCHAR(255),
			block_number     BIGINT,
			block_timestamp  TIMESTAMPTZ,
			confirmations    INT           NOT NULL DEFAULT 0,
			last_synced_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			created_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

			UNIQUE(chain_id, tx_hash, output_index)
		);

		CREATE INDEX IF NOT EXISTS idx_wallet_utxos_wallet_id
			ON wallet_utxos(wallet_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_utxos_chain_id
			ON wallet_utxos(chain_id);
		CREATE INDEX IF NOT EXISTS idx_wallet_utxos_status
			ON wallet_utxos(status);
	`)
	return err
}

func (r *M20260407000006CreateWalletUtxosTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_utxos`)
	return err
}
