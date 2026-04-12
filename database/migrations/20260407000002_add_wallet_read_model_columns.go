package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000002AddWalletReadModelColumns struct{}

func (r *M20260407000002AddWalletReadModelColumns) Signature() string {
	return "20260407000002_add_wallet_read_model_columns"
}

func (r *M20260407000002AddWalletReadModelColumns) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE wallets
			ALTER COLUMN status DROP DEFAULT;
		ALTER TABLE wallets
			ALTER COLUMN status TYPE wallet_status USING status::wallet_status;
		ALTER TABLE wallets
			ALTER COLUMN status SET DEFAULT 'active';

		ALTER TABLE wallets
			ADD COLUMN IF NOT EXISTS balance_asset VARCHAR(32),
			ADD COLUMN IF NOT EXISTS balance_raw TEXT,
			ADD COLUMN IF NOT EXISTS balance_display TEXT,
			ADD COLUMN IF NOT EXISTS balance_usd NUMERIC(28, 10),
			ADD COLUMN IF NOT EXISTS balance_last_synced_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS read_model_status wallet_read_model_status DEFAULT 'idle';
	`)
	return err
}

func (r *M20260407000002AddWalletReadModelColumns) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE wallets
			DROP COLUMN IF EXISTS read_model_status,
			DROP COLUMN IF EXISTS balance_last_synced_at,
			DROP COLUMN IF EXISTS balance_usd,
			DROP COLUMN IF EXISTS balance_display,
			DROP COLUMN IF EXISTS balance_raw,
			DROP COLUMN IF EXISTS balance_asset;

		ALTER TABLE wallets
			ALTER COLUMN status TYPE VARCHAR(20) USING status::text;
	`)
	return err
}
