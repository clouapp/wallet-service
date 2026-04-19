package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260418000009UsersPreferencesDefault struct{}

func (r *M20260418000009UsersPreferencesDefault) Signature() string {
	return "20260418000009_users_preferences_default"
}

// Up re-asserts the jsonb '{}' default on users.preferences. The original
// `20260411000002_add_preferences_to_users` migration already set this
// default, but subsequent schema churn or manual interventions can strip
// defaults; this migration is idempotent and guarantees every INSERT that
// omits preferences succeeds without relying on application code.
func (r *M20260418000009UsersPreferencesDefault) Up() error {
	stmts := []string{
		`ALTER TABLE users ALTER COLUMN preferences SET DEFAULT '{}'::jsonb`,
		`UPDATE users SET preferences = '{}'::jsonb WHERE preferences IS NULL`,
		`ALTER TABLE users ALTER COLUMN preferences SET NOT NULL`,
	}
	for _, s := range stmts {
		if _, err := facades.Orm().Query().Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *M20260418000009UsersPreferencesDefault) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE users ALTER COLUMN preferences DROP DEFAULT`)
	return err
}
