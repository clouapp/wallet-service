package migrations

import "github.com/goravel/framework/facades"

type M00000000000020CreateAccountsTable struct{}

func (r *M00000000000020CreateAccountsTable) Signature() string {
	return "00000000000020_create_accounts_table"
}

func (r *M00000000000020CreateAccountsTable) Up() error {
	stmts := []string{
		`CREATE TABLE accounts (
			id                UUID        NOT NULL,
			name              VARCHAR(255) NOT NULL,
			status            VARCHAR(20)  NOT NULL DEFAULT 'active',
			view_all_wallets  BOOLEAN      NOT NULL DEFAULT FALSE,
			environment       VARCHAR(4)   NOT NULL DEFAULT 'prod',
			linked_account_id UUID,
			created_at        TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at        TIMESTAMP(0) WITHOUT TIME ZONE,
			sweep_limits      JSONB,
			CONSTRAINT accounts_pkey PRIMARY KEY (id),
			CONSTRAINT accounts_linked_account_id_foreign FOREIGN KEY (linked_account_id) REFERENCES accounts(id) ON DELETE SET NULL
		)`,
		`COMMENT ON COLUMN accounts.sweep_limits IS 'Per-account overrides for sweep rate limits and velocity caps'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000020CreateAccountsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS accounts`)
	return err
}
