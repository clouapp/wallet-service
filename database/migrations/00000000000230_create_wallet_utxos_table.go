package migrations

import "github.com/goravel/framework/facades"

type M00000000000230CreateWalletUtxosTable struct{}

func (r *M00000000000230CreateWalletUtxosTable) Signature() string {
	return "00000000000230_create_wallet_utxos_table"
}

func (r *M00000000000230CreateWalletUtxosTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallet_utxos (
			id               UUID         NOT NULL DEFAULT gen_random_uuid(),
			wallet_id        UUID         NOT NULL,
			address_id       UUID,
			chain_id         VARCHAR(32)  NOT NULL,
			tx_hash          VARCHAR(255) NOT NULL,
			output_index     INTEGER      NOT NULL,
			address          TEXT         NOT NULL,
			value_raw        TEXT         NOT NULL,
			script_pub_key   TEXT,
			status           utxo_status  NOT NULL,
			spent_by_tx_hash VARCHAR(255),
			block_number     BIGINT,
			block_timestamp  TIMESTAMPTZ,
			confirmations    INTEGER      NOT NULL DEFAULT 0,
			last_synced_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			CONSTRAINT wallet_utxos_pkey                              PRIMARY KEY (id),
			CONSTRAINT wallet_utxos_chain_id_tx_hash_output_index_key UNIQUE (chain_id, tx_hash, output_index),
			CONSTRAINT wallet_utxos_wallet_id_fkey                    FOREIGN KEY (wallet_id)  REFERENCES wallets(id),
			CONSTRAINT wallet_utxos_address_id_fkey                   FOREIGN KEY (address_id) REFERENCES addresses(id),
			CONSTRAINT wallet_utxos_chain_id_fkey                     FOREIGN KEY (chain_id)   REFERENCES chains(id)
		)`,
		`CREATE INDEX idx_wallet_utxos_wallet_id ON wallet_utxos (wallet_id)`,
		`CREATE INDEX idx_wallet_utxos_chain_id  ON wallet_utxos (chain_id)`,
		`CREATE INDEX idx_wallet_utxos_status    ON wallet_utxos (status)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000230CreateWalletUtxosTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallet_utxos`)
	return err
}
