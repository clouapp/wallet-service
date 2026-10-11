package migrations

// M00000000000600AddDestinationTag adds the XRP Ledger destination tag a
// withdrawal was requested with (withdrawals) and the one its payment carried
// (transactions), next to destination_address and to_address. NULL means no tag;
// a tag is an unsigned 32-bit integer.
type M00000000000600AddDestinationTag struct{}

func (r *M00000000000600AddDestinationTag) Signature() string {
	return "00000000000600_add_destination_tag"
}

func (r *M00000000000600AddDestinationTag) Up() error {
	return execMigrationSQL(`
		ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS destination_tag BIGINT NULL;
		ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_destination_tag_uint32;
		ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_destination_tag_uint32
			CHECK (destination_tag BETWEEN 0 AND 4294967295);
		ALTER TABLE transactions ADD COLUMN IF NOT EXISTS destination_tag BIGINT NULL;
		ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_destination_tag_uint32;
		ALTER TABLE transactions ADD CONSTRAINT transactions_destination_tag_uint32
			CHECK (destination_tag BETWEEN 0 AND 4294967295);
	`)
}

func (r *M00000000000600AddDestinationTag) Down() error {
	return execMigrationSQL(`
		ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_destination_tag_uint32;
		ALTER TABLE transactions DROP COLUMN IF EXISTS destination_tag;
		ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_destination_tag_uint32;
		ALTER TABLE withdrawals DROP COLUMN IF EXISTS destination_tag;
	`)
}
