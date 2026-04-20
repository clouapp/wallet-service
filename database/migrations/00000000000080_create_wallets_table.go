package migrations

import "github.com/goravel/framework/facades"

type M00000000000080CreateWalletsTable struct{}

func (r *M00000000000080CreateWalletsTable) Signature() string {
	return "00000000000080_create_wallets_table"
}

func (r *M00000000000080CreateWalletsTable) Up() error {
	stmts := []string{
		`CREATE TABLE wallets (
			id                     UUID         NOT NULL,
			chain                  VARCHAR(50)  NOT NULL,
			label                  VARCHAR(255),
			mpc_customer_share     TEXT         NOT NULL,
			mpc_share_iv           TEXT         NOT NULL,
			mpc_share_salt         TEXT         NOT NULL,
			mpc_secret_arn         TEXT         NOT NULL,
			mpc_public_key         TEXT         NOT NULL,
			mpc_curve              VARCHAR(20)  NOT NULL,
			account_id             UUID,
			status                 wallet_status NOT NULL DEFAULT 'active',
			fee_rate_min           INTEGER,
			fee_rate_max           INTEGER,
			fee_multiplier         NUMERIC(8,4),
			required_approvals     INTEGER      NOT NULL DEFAULT 1,
			frozen_until           TIMESTAMPTZ,
			activation_code        VARCHAR(6),
			created_at             TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at             TIMESTAMP(0) WITHOUT TIME ZONE,
			deposit_address_id     UUID,
			balance_asset          VARCHAR(32),
			balance_raw            TEXT,
			balance_display        TEXT,
			balance_usd            NUMERIC(28,10),
			balance_last_synced_at TIMESTAMPTZ,
			read_model_status      wallet_read_model_status DEFAULT 'idle',
			address_index          INTEGER      NOT NULL DEFAULT 0,
			mpc_chain_code         TEXT,
			gas_status             wallet_gas_status NOT NULL DEFAULT 'unseeded',
			gas_last_checked_at    TIMESTAMPTZ,
			sweep_policy_version   INTEGER      NOT NULL DEFAULT 1,
			CONSTRAINT wallets_pkey PRIMARY KEY (id)
		)`,
		`CREATE INDEX wallets_chain_index      ON wallets (chain)`,
		`CREATE INDEX wallets_gas_status_index ON wallets (gas_status)`,
		`COMMENT ON TABLE  wallets                       IS 'MPC co-signing wallets'`,
		`COMMENT ON COLUMN wallets.chain                 IS 'Blockchain identifier (eth, polygon, sol, btc)'`,
		`COMMENT ON COLUMN wallets.label                 IS 'User-friendly wallet label'`,
		`COMMENT ON COLUMN wallets.mpc_customer_share    IS 'Hex-encoded AES-256-GCM encrypted share_A (ciphertext || 16-byte tag)'`,
		`COMMENT ON COLUMN wallets.mpc_share_iv          IS 'Hex-encoded AES-256-GCM nonce, exactly 12 bytes'`,
		`COMMENT ON COLUMN wallets.mpc_share_salt        IS 'Hex-encoded Argon2id salt, exactly 16 bytes'`,
		`COMMENT ON COLUMN wallets.mpc_secret_arn        IS 'AWS Secrets Manager ARN for share_B'`,
		`COMMENT ON COLUMN wallets.mpc_public_key        IS 'Hex-encoded compressed public key (33 bytes secp256k1 / 32 bytes ed25519)'`,
		`COMMENT ON COLUMN wallets.mpc_curve             IS 'secp256k1 or ed25519'`,
		`COMMENT ON COLUMN wallets.address_index         IS 'Next derivation index for child addresses'`,
		`COMMENT ON COLUMN wallets.mpc_chain_code        IS 'Hex-encoded 32-byte chain code for BIP-32/SLIP-0010 derivation'`,
		`COMMENT ON COLUMN wallets.gas_status            IS 'unseeded | seeded | low'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000080CreateWalletsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS wallets`)
	return err
}
