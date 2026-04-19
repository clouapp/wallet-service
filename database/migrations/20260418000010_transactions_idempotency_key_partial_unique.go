package migrations

import "github.com/goravel/framework/facades"

// M20260418000010TransactionsIdempotencyKeyPartialUnique replaces the naive
// UNIQUE constraint on transactions.idempotency_key with a partial unique
// index that only covers non-NULL values.
//
// The sweep executor creates multiple transaction rows (sweep + gas_seed +
// withdrawal) inside a single multi_sweep plan without an idempotency key.
// With the old full UNIQUE constraint, those rows could collide on the
// implicit empty string '' value emitted by some ORM drivers, leaving the
// wallet in a partially-swept state. Making the column nullable and
// restricting uniqueness to non-NULL values lets those rows coexist while
// preserving idempotency for user-driven withdrawals that actually set a key.
type M20260418000010TransactionsIdempotencyKeyPartialUnique struct{}

func (r *M20260418000010TransactionsIdempotencyKeyPartialUnique) Signature() string {
	return "20260418000010_transactions_idempotency_key_partial_unique"
}

func (r *M20260418000010TransactionsIdempotencyKeyPartialUnique) Up() error {
	stmts := []string{
		// Drop the existing UNIQUE constraint. Name confirmed via `\d transactions`
		// on the dev database; Goravel's schema builder emitted this name for the
		// `unique` gorm tag on the original create-transactions migration.
		`ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_idempotency_key_unique`,
		// Also drop any same-named index left behind, defensive across environments.
		`DROP INDEX IF EXISTS transactions_idempotency_key_unique`,
		// Normalize empty strings to NULL so the partial index treats them as absent.
		`UPDATE transactions SET idempotency_key = NULL WHERE idempotency_key = ''`,
		// Partial unique index — multiple NULLs coexist, non-NULLs stay unique.
		`CREATE UNIQUE INDEX IF NOT EXISTS transactions_idempotency_key_unique
			ON transactions (idempotency_key)
			WHERE idempotency_key IS NOT NULL`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M20260418000010TransactionsIdempotencyKeyPartialUnique) Down() error {
	stmts := []string{
		`DROP INDEX IF EXISTS transactions_idempotency_key_unique`,
		`ALTER TABLE transactions ADD CONSTRAINT transactions_idempotency_key_unique UNIQUE (idempotency_key)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}
