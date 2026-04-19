package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260418000008NormalizeTimestamptzRemainder struct{}

func (r *M20260418000008NormalizeTimestamptzRemainder) Signature() string {
	return "20260418000008_normalize_timestamptz_remainder"
}

func (r *M20260418000008NormalizeTimestamptzRemainder) Up() error {
	// Normalize the remaining tz-naive business-event timestamp columns to
	// timestamptz. Existing rows are interpreted as UTC (no offset shift).
	// Goravel's Schema builder Timestamp() always emits `timestamp without time
	// zone`, so we use raw SQL.
	stmts := []string{
		`ALTER TABLE transactions          ALTER COLUMN confirmed_at   TYPE TIMESTAMPTZ USING confirmed_at   AT TIME ZONE 'UTC'`,
		`ALTER TABLE password_reset_tokens ALTER COLUMN expires_at     TYPE TIMESTAMPTZ USING expires_at     AT TIME ZONE 'UTC'`,
		`ALTER TABLE password_reset_tokens ALTER COLUMN used_at        TYPE TIMESTAMPTZ USING used_at        AT TIME ZONE 'UTC'`,
		`ALTER TABLE access_tokens         ALTER COLUMN valid_until    TYPE TIMESTAMPTZ USING valid_until    AT TIME ZONE 'UTC'`,
		`ALTER TABLE totp_recovery_codes   ALTER COLUMN used_at        TYPE TIMESTAMPTZ USING used_at        AT TIME ZONE 'UTC'`,
		`ALTER TABLE refresh_tokens        ALTER COLUMN expires_at     TYPE TIMESTAMPTZ USING expires_at     AT TIME ZONE 'UTC'`,
		`ALTER TABLE refresh_tokens        ALTER COLUMN revoked_at     TYPE TIMESTAMPTZ USING revoked_at     AT TIME ZONE 'UTC'`,
		`ALTER TABLE webhook_events        ALTER COLUMN delivered_at   TYPE TIMESTAMPTZ USING delivered_at   AT TIME ZONE 'UTC'`,
		`ALTER TABLE webhook_subscriptions ALTER COLUMN last_synced_at TYPE TIMESTAMPTZ USING last_synced_at AT TIME ZONE 'UTC'`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M20260418000008NormalizeTimestamptzRemainder) Down() error {
	stmts := []string{
		`ALTER TABLE transactions          ALTER COLUMN confirmed_at   TYPE TIMESTAMP`,
		`ALTER TABLE password_reset_tokens ALTER COLUMN expires_at     TYPE TIMESTAMP`,
		`ALTER TABLE password_reset_tokens ALTER COLUMN used_at        TYPE TIMESTAMP`,
		`ALTER TABLE access_tokens         ALTER COLUMN valid_until    TYPE TIMESTAMP`,
		`ALTER TABLE totp_recovery_codes   ALTER COLUMN used_at        TYPE TIMESTAMP`,
		`ALTER TABLE refresh_tokens        ALTER COLUMN expires_at     TYPE TIMESTAMP`,
		`ALTER TABLE refresh_tokens        ALTER COLUMN revoked_at     TYPE TIMESTAMP`,
		`ALTER TABLE webhook_events        ALTER COLUMN delivered_at   TYPE TIMESTAMP`,
		`ALTER TABLE webhook_subscriptions ALTER COLUMN last_synced_at TYPE TIMESTAMP`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}
