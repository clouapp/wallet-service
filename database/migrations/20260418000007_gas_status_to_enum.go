package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260418000007GasStatusToEnum struct{}

func (r *M20260418000007GasStatusToEnum) Signature() string {
	return "20260418000007_gas_status_to_enum"
}

func (r *M20260418000007GasStatusToEnum) Up() error {
	if _, err := facades.Orm().Query().Exec(`
		DO $$ BEGIN
			CREATE TYPE wallet_gas_status AS ENUM ('unseeded', 'seeded', 'low');
		EXCEPTION WHEN duplicate_object THEN null;
		END $$;
	`); err != nil {
		return err
	}
	// Drop default before type change; convert column; restore default.
	stmts := []string{
		`ALTER TABLE wallets ALTER COLUMN gas_status DROP DEFAULT`,
		`ALTER TABLE wallets ALTER COLUMN gas_status TYPE wallet_gas_status USING gas_status::wallet_gas_status`,
		`ALTER TABLE wallets ALTER COLUMN gas_status SET DEFAULT 'unseeded'::wallet_gas_status`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M20260418000007GasStatusToEnum) Down() error {
	stmts := []string{
		`ALTER TABLE wallets ALTER COLUMN gas_status DROP DEFAULT`,
		`ALTER TABLE wallets ALTER COLUMN gas_status TYPE varchar(16) USING gas_status::varchar`,
		`ALTER TABLE wallets ALTER COLUMN gas_status SET DEFAULT 'unseeded'`,
		`DROP TYPE IF EXISTS wallet_gas_status`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}
