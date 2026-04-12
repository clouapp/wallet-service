package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260412000001AddAddressDerivationColumns struct{}

func (r *M20260412000001AddAddressDerivationColumns) Signature() string {
	return "20260412000001_add_address_derivation_columns"
}

func (r *M20260412000001AddAddressDerivationColumns) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE wallets
			ADD COLUMN IF NOT EXISTS address_index INTEGER NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS mpc_chain_code TEXT;

		ALTER TABLE addresses
			ADD COLUMN IF NOT EXISTS derivation_type VARCHAR(20) NOT NULL DEFAULT 'genesis',
			ADD COLUMN IF NOT EXISTS encrypted_private_key TEXT,
			ADD COLUMN IF NOT EXISTS encryption_iv TEXT,
			ADD COLUMN IF NOT EXISTS encryption_salt TEXT;

		COMMENT ON COLUMN wallets.address_index IS 'Next derivation index for child addresses';
		COMMENT ON COLUMN wallets.mpc_chain_code IS 'Hex-encoded 32-byte chain code for BIP-32/SLIP-0010 derivation';
		COMMENT ON COLUMN addresses.derivation_type IS 'genesis (wallet creation), bip32 (secp256k1 derived), slip0010 (ed25519 derived)';
		COMMENT ON COLUMN addresses.encrypted_private_key IS 'AES-256-GCM encrypted child private key (only for slip0010 addresses)';
		COMMENT ON COLUMN addresses.encryption_iv IS 'Hex-encoded 12-byte GCM nonce for encrypted_private_key';
		COMMENT ON COLUMN addresses.encryption_salt IS 'Hex-encoded 16-byte Argon2id salt for encrypted_private_key';
	`)
	return err
}

func (r *M20260412000001AddAddressDerivationColumns) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE addresses
			DROP COLUMN IF EXISTS encryption_salt,
			DROP COLUMN IF EXISTS encryption_iv,
			DROP COLUMN IF EXISTS encrypted_private_key,
			DROP COLUMN IF EXISTS derivation_type;

		ALTER TABLE wallets
			DROP COLUMN IF EXISTS mpc_chain_code,
			DROP COLUMN IF EXISTS address_index;
	`)
	return err
}
