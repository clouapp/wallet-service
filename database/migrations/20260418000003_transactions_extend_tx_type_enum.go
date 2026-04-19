package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260418000003TransactionsExtendTxTypeEnum struct{}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Signature() string {
	return "20260418000003_transactions_extend_tx_type_enum"
}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Up() error {
	if _, err := facades.Orm().Query().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'sweep'`); err != nil {
		return err
	}
	if _, err := facades.Orm().Query().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'gas_seed'`); err != nil {
		return err
	}
	return nil
}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Down() error {
	// Postgres does not support DROP VALUE from enum types; revert requires full recreate.
	// No-op is safe for this project — flag for manual intervention if rollback is needed.
	return nil
}
