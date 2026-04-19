package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260418000006NormalizeTimestamptz struct{}

func (r *M20260418000006NormalizeTimestamptz) Signature() string {
	return "20260418000006_normalize_timestamptz"
}

func (r *M20260418000006NormalizeTimestamptz) Up() error {
	// Convert tz-naive timestamp columns to timestamptz (UTC interpretation).
	// Uses the raw SQL escape hatch because Goravel's Schema builder Timestamp()
	// always emits `timestamp without time zone`.
	stmts := []string{
		`ALTER TABLE wallets ALTER COLUMN gas_last_checked_at TYPE TIMESTAMPTZ USING gas_last_checked_at AT TIME ZONE 'UTC'`,
		`ALTER TABLE wallets ALTER COLUMN frozen_until        TYPE TIMESTAMPTZ USING frozen_until        AT TIME ZONE 'UTC'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M20260418000006NormalizeTimestamptz) Down() error {
	stmts := []string{
		`ALTER TABLE wallets ALTER COLUMN gas_last_checked_at TYPE TIMESTAMP`,
		`ALTER TABLE wallets ALTER COLUMN frozen_until        TYPE TIMESTAMP`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}
