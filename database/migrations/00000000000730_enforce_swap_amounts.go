package migrations

// M00000000000730EnforceSwapAmounts adds monetary checks after the schema
// builder transaction creating the swap tables has committed. Mixing its DDL
// with ORM raw SQL would use a separate connection before those tables exist.
type M00000000000730EnforceSwapAmounts struct{}

func (*M00000000000730EnforceSwapAmounts) Signature() string {
	return "00000000000730_enforce_swap_amounts"
}
func (*M00000000000730EnforceSwapAmounts) Up() error {
	return execMigrationSQL(`DO $$ BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'swap_quotes_expected_positive') THEN
	ALTER TABLE swap_quotes ADD CONSTRAINT swap_quotes_expected_positive CHECK (expected_raw ~ '^[0-9]+$' AND expected_raw::numeric > 0);
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'swap_quotes_minimum_positive') THEN
	ALTER TABLE swap_quotes ADD CONSTRAINT swap_quotes_minimum_positive CHECK (minimum_raw ~ '^[0-9]+$' AND minimum_raw::numeric > 0 AND minimum_raw::numeric <= expected_raw::numeric);
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'swap_orders_received_non_negative') THEN
	ALTER TABLE swap_orders ADD CONSTRAINT swap_orders_received_non_negative CHECK (received_raw ~ '^[0-9]+$');
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'swap_orders_refund_non_negative') THEN
	ALTER TABLE swap_orders ADD CONSTRAINT swap_orders_refund_non_negative CHECK (refund_raw ~ '^[0-9]+$');
	END IF;
	END $$;`)
}
func (*M00000000000730EnforceSwapAmounts) Down() error {
	return execMigrationSQL(`ALTER TABLE swap_quotes DROP CONSTRAINT IF EXISTS swap_quotes_expected_positive, DROP CONSTRAINT IF EXISTS swap_quotes_minimum_positive; ALTER TABLE swap_orders DROP CONSTRAINT IF EXISTS swap_orders_received_non_negative, DROP CONSTRAINT IF EXISTS swap_orders_refund_non_negative;`)
}
