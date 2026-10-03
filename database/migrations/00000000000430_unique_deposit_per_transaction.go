package migrations

import (
	"fmt"

	"github.com/goravel/framework/facades"
)

// M00000000000430UniqueDepositPerTransaction makes the database, not a read-then-insert
// check, guarantee one deposit row per (chain, tx_hash, log_index): a block processed
// twice (scanner retry, pending-block reprocessor, targeted scan, two processes at once)
// can never record a deposit, or its deposit webhooks, twice. Block-scanner deposits
// use log_index -1, so they are unique per chain and transaction.
//
// Up refuses to run while duplicates exist, naming how many, instead of picking one.
type M00000000000430UniqueDepositPerTransaction struct{}

const uniqueDepositIndex = "transactions_deposit_chain_tx_log_unique"

func (r *M00000000000430UniqueDepositPerTransaction) Signature() string {
	return "00000000000430_unique_deposit_per_transaction"
}

func (r *M00000000000430UniqueDepositPerTransaction) Up() error {
	var duplicates []struct{ DuplicateGroups int64 }
	if err := facades.Orm().Query().Raw(`
		SELECT COUNT(*) AS duplicate_groups FROM (
			SELECT 1 FROM transactions
			WHERE tx_type = 'deposit'
			GROUP BY chain, tx_hash, log_index
			HAVING COUNT(*) > 1
		) duplicated`).Scan(&duplicates); err != nil {
		return fmt.Errorf("count duplicate deposits: %w", err)
	}
	if len(duplicates) == 1 && duplicates[0].DuplicateGroups > 0 {
		return fmt.Errorf("%d (chain, tx_hash, log_index) deposit groups have more than one row; resolve them before adding %s", duplicates[0].DuplicateGroups, uniqueDepositIndex)
	}
	_, err := facades.Orm().Query().Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ` + uniqueDepositIndex +
		` ON transactions (chain, tx_hash, log_index) WHERE tx_type = 'deposit'`)
	return err
}

func (r *M00000000000430UniqueDepositPerTransaction) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP INDEX IF EXISTS ` + uniqueDepositIndex)
	return err
}
