package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260407000005ExtendTransactionsForWalletReads struct{}

func (r *M20260407000005ExtendTransactionsForWalletReads) Signature() string {
	return "20260407000005_extend_transactions_for_wallet_reads"
}

func (r *M20260407000005ExtendTransactionsForWalletReads) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE transactions
			ALTER COLUMN tx_type TYPE transaction_type USING tx_type::transaction_type,
			ALTER COLUMN status TYPE transaction_status USING status::transaction_status,
			ADD COLUMN IF NOT EXISTS direction transaction_direction,
			ADD COLUMN IF NOT EXISTS source transaction_source,
			ADD COLUMN IF NOT EXISTS raw_payload JSONB,
			ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ;
	`)
	return err
}

func (r *M20260407000005ExtendTransactionsForWalletReads) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE transactions
			DROP COLUMN IF EXISTS synced_at,
			DROP COLUMN IF EXISTS raw_payload,
			DROP COLUMN IF EXISTS source,
			DROP COLUMN IF EXISTS direction,
			ALTER COLUMN status TYPE varchar(20) USING status::text,
			ALTER COLUMN tx_type TYPE varchar(20) USING tx_type::text;
	`)
	return err
}
