package migrations

import "github.com/goravel/framework/facades"

type M00000000000090CreateAddressesTable struct{}

func (r *M00000000000090CreateAddressesTable) Signature() string {
	return "00000000000090_create_addresses_table"
}

func (r *M00000000000090CreateAddressesTable) Up() error {
	stmts := []string{
		`CREATE TABLE addresses (
			id                    UUID         NOT NULL,
			wallet_id             UUID         NOT NULL,
			chain                 VARCHAR(50)  NOT NULL,
			address               VARCHAR(255) NOT NULL,
			derivation_index      INTEGER      NOT NULL,
			external_user_id      VARCHAR(255) NOT NULL,
			metadata              TEXT,
			is_active             BOOLEAN      NOT NULL DEFAULT TRUE,
			label                 VARCHAR(255),
			created_by            UUID,
			created_at            TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at            TIMESTAMP(0) WITHOUT TIME ZONE,
			derivation_type       VARCHAR(20)  NOT NULL DEFAULT 'genesis',
			encrypted_private_key TEXT,
			encryption_iv         TEXT,
			encryption_salt       TEXT,
			CONSTRAINT addresses_pkey            PRIMARY KEY (id),
			CONSTRAINT addresses_address_unique  UNIQUE (address),
			CONSTRAINT addresses_wallet_id_foreign FOREIGN KEY (wallet_id) REFERENCES wallets(id)
		)`,
		`CREATE INDEX addresses_wallet_id_index        ON addresses (wallet_id)`,
		`CREATE INDEX addresses_external_user_id_index ON addresses (external_user_id)`,
		`CREATE INDEX addresses_is_active_index        ON addresses (is_active)`,
		`CREATE INDEX addresses_chain_address_index    ON addresses (chain, address)`,
		`COMMENT ON TABLE  addresses                       IS 'Derived addresses for deposit scanning'`,
		`COMMENT ON COLUMN addresses.wallet_id             IS 'Foreign key to wallets table'`,
		`COMMENT ON COLUMN addresses.chain                 IS 'Blockchain identifier'`,
		`COMMENT ON COLUMN addresses.address               IS 'Blockchain address'`,
		`COMMENT ON COLUMN addresses.derivation_index      IS 'HD derivation index (m/44''/60''/0''/0/{index})'`,
		`COMMENT ON COLUMN addresses.external_user_id      IS 'Client''s user identifier'`,
		`COMMENT ON COLUMN addresses.metadata              IS 'Optional JSON metadata'`,
		`COMMENT ON COLUMN addresses.is_active             IS 'Whether address is active for deposits'`,
		`COMMENT ON COLUMN addresses.derivation_type       IS 'genesis (wallet creation), bip32 (secp256k1 derived), slip0010 (ed25519 derived)'`,
		`COMMENT ON COLUMN addresses.encrypted_private_key IS 'AES-256-GCM encrypted child private key (only for slip0010 addresses)'`,
		`COMMENT ON COLUMN addresses.encryption_iv         IS 'Hex-encoded 12-byte GCM nonce for encrypted_private_key'`,
		`COMMENT ON COLUMN addresses.encryption_salt       IS 'Hex-encoded 16-byte Argon2id salt for encrypted_private_key'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000090CreateAddressesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS addresses`)
	return err
}
