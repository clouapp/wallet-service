package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260411000001CreateCurrenciesTable struct{}

func (r *M20260411000001CreateCurrenciesTable) Signature() string {
	return "20260411000001_create_currencies_table"
}

func (r *M20260411000001CreateCurrenciesTable) Up() error {
	_, err := facades.Orm().Query().Exec(`
		CREATE TYPE currency_type AS ENUM ('crypto', 'fiat');

		CREATE TABLE IF NOT EXISTS currencies (
			id               UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
			name             VARCHAR(100)    NOT NULL,
			code             VARCHAR(20)     NOT NULL UNIQUE,
			symbol           VARCHAR(10),
			type             currency_type   NOT NULL,
			logo             VARCHAR(500),
			subunits         INT             NOT NULL DEFAULT 2,
			current_price    DECIMAL(28,10)  NOT NULL DEFAULT 1,
			last_price       DECIMAL(28,10),
			price_updated_at TIMESTAMPTZ,
			active           BOOLEAN         NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ     NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_currencies_type_active ON currencies(type, active);
		CREATE INDEX IF NOT EXISTS idx_currencies_code ON currencies(code);
	`)
	return err
}

func (r *M20260411000001CreateCurrenciesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`
		DROP TABLE IF EXISTS currencies;
		DROP TYPE IF EXISTS currency_type;
	`)
	return err
}
