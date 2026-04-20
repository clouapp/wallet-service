package migrations

import "github.com/goravel/framework/facades"

type M00000000000180CreateWithdrawalsTable struct{}

func (r *M00000000000180CreateWithdrawalsTable) Signature() string {
	return "00000000000180_create_withdrawals_table"
}

func (r *M00000000000180CreateWithdrawalsTable) Up() error {
	stmts := []string{
		`CREATE TABLE withdrawals (
			id                   UUID           NOT NULL,
			wallet_id            UUID           NOT NULL,
			transaction_id       UUID,
			account_id           UUID,
			status               VARCHAR(20)    NOT NULL DEFAULT 'pending',
			amount               NUMERIC(36,18) NOT NULL,
			destination_address  TEXT           NOT NULL,
			fee_estimate         NUMERIC(36,18),
			note                 TEXT,
			created_by           UUID,
			created_at           TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at           TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT withdrawals_pkey              PRIMARY KEY (id),
			CONSTRAINT withdrawals_wallet_id_foreign FOREIGN KEY (wallet_id) REFERENCES wallets(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX withdrawals_wallet_id_index  ON withdrawals (wallet_id)`,
		`CREATE INDEX withdrawals_account_id_index ON withdrawals (account_id)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000180CreateWithdrawalsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS withdrawals`)
	return err
}
