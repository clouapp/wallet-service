package migrations

// M00000000000710AddUnconsolidatedBalances stores, beside each wallet asset balance
// (the wallet's base address), what the wallet's other deposit addresses hold of
// that asset and have not been consolidated into the base yet: the sum in base units
// (unconsolidated_raw) and as a decimal (unconsolidated_display), and how many
// addresses hold it (unconsolidated_addresses). Zero until the next balance refresh.
// The amounts are never negative, like amount_raw and amount_display (migration 280).
type M00000000000710AddUnconsolidatedBalances struct{}

func (r *M00000000000710AddUnconsolidatedBalances) Signature() string {
	return "00000000000710_add_unconsolidated_balances"
}

func (r *M00000000000710AddUnconsolidatedBalances) Up() error {
	_, err := migrationQuery().Exec(`ALTER TABLE wallet_asset_balances
		ADD COLUMN IF NOT EXISTS unconsolidated_raw TEXT NOT NULL DEFAULT '0'
			CONSTRAINT wallet_asset_balances_unconsolidated_raw_non_negative CHECK (btrim(unconsolidated_raw) NOT LIKE '-%'),
		ADD COLUMN IF NOT EXISTS unconsolidated_display TEXT NOT NULL DEFAULT '0'
			CONSTRAINT wallet_asset_balances_unconsolidated_display_non_negative CHECK (btrim(unconsolidated_display) NOT LIKE '-%'),
		ADD COLUMN IF NOT EXISTS unconsolidated_addresses INTEGER NOT NULL DEFAULT 0
			CONSTRAINT wallet_asset_balances_unconsolidated_addresses_non_negative CHECK (unconsolidated_addresses >= 0)`)
	return err
}

func (r *M00000000000710AddUnconsolidatedBalances) Down() error {
	_, err := migrationQuery().Exec(`ALTER TABLE wallet_asset_balances
		DROP COLUMN IF EXISTS unconsolidated_addresses,
		DROP COLUMN IF EXISTS unconsolidated_display,
		DROP COLUMN IF EXISTS unconsolidated_raw`)
	return err
}
