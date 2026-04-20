package migrations

import "github.com/goravel/framework/facades"

type M00000000000070CreateCurrenciesTable struct{}

func (r *M00000000000070CreateCurrenciesTable) Signature() string {
	return "00000000000070_create_currencies_table"
}

func (r *M00000000000070CreateCurrenciesTable) Up() error {
	stmts := []string{
		`CREATE TABLE currencies (
			id               UUID           NOT NULL DEFAULT gen_random_uuid(),
			name             VARCHAR(100)   NOT NULL,
			code             VARCHAR(20)    NOT NULL,
			symbol           VARCHAR(10),
			type             currency_type  NOT NULL,
			logo             VARCHAR(500),
			subunits         INTEGER        NOT NULL DEFAULT 2,
			current_price    NUMERIC(28,10) NOT NULL DEFAULT 1,
			last_price       NUMERIC(28,10),
			price_updated_at TIMESTAMPTZ,
			active           BOOLEAN        NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
			CONSTRAINT currencies_pkey     PRIMARY KEY (id),
			CONSTRAINT currencies_code_key UNIQUE (code)
		)`,
		`CREATE INDEX idx_currencies_type_active ON currencies (type, active)`,
		`CREATE INDEX idx_currencies_code        ON currencies (code)`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000070CreateCurrenciesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS currencies`)
	return err
}
