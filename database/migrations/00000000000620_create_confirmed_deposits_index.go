package migrations

// M00000000000620CreateConfirmedDepositsIndex indexes the confirmed deposits by
// confirmation time. The confirmation tracker looks, every minute, for deposits
// confirmed in the last two days whose deposit.confirmed webhook was never enqueued;
// the partial index keeps that read to the deposits of the window. A partial index
// is raw SQL: the schema builder does not express its WHERE.
type M00000000000620CreateConfirmedDepositsIndex struct{}

func (r *M00000000000620CreateConfirmedDepositsIndex) Signature() string {
	return "00000000000620_create_confirmed_deposits_index"
}

func (r *M00000000000620CreateConfirmedDepositsIndex) Up() error {
	return execMigrationSQL(`
		CREATE INDEX IF NOT EXISTS transactions_confirmed_deposits_index
			ON transactions (confirmed_at)
			WHERE tx_type = 'deposit' AND status = 'confirmed';
	`)
}

func (r *M00000000000620CreateConfirmedDepositsIndex) Down() error {
	return execMigrationSQL(`DROP INDEX IF EXISTS transactions_confirmed_deposits_index;`)
}
