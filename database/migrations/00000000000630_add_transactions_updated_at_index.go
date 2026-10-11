package migrations

import "fmt"

// transactionsUpdatedAtIndex serves the changes feed of GET /api/v1/transactions,
// which reads an account's transactions in (updated_at, id) order after a cursor.
const transactionsUpdatedAtIndex = "transactions_updated_at_id_index"

// M00000000000630AddTransactionsUpdatedAtIndex indexes transactions on (updated_at, id)
// for the changes feed. A row without updated_at would never reach that feed, so
// such rows first take their created_at (or the migration time when that is missing
// too); the ORM stamps both on every insert, so none is expected. The SQL is raw,
// like the migrations before it: the import-direction check refuses
// database/migrations → app/facades, which the schema builder would need.
type M00000000000630AddTransactionsUpdatedAtIndex struct{}

func (r *M00000000000630AddTransactionsUpdatedAtIndex) Signature() string {
	return "00000000000630_add_transactions_updated_at_index"
}

func (r *M00000000000630AddTransactionsUpdatedAtIndex) Up() error {
	if _, err := migrationQuery().Exec(
		`UPDATE transactions SET updated_at = COALESCE(created_at, NOW()) WHERE updated_at IS NULL`,
	); err != nil {
		return fmt.Errorf("backfill transactions.updated_at: %w", err)
	}
	_, err := migrationQuery().Exec(`CREATE INDEX IF NOT EXISTS ` + transactionsUpdatedAtIndex +
		` ON transactions (updated_at, id)`)
	return err
}

func (r *M00000000000630AddTransactionsUpdatedAtIndex) Down() error {
	_, err := migrationQuery().Exec(`DROP INDEX IF EXISTS ` + transactionsUpdatedAtIndex)
	return err
}
