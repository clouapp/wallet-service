package migrations

import (
	"github.com/goravel/framework/facades"
)

type M20260411000002AddPreferencesToUsers struct{}

func (r *M20260411000002AddPreferencesToUsers) Signature() string {
	return "20260411000002_add_preferences_to_users"
}

func (r *M20260411000002AddPreferencesToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE users ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL DEFAULT '{}';
	`)
	return err
}

func (r *M20260411000002AddPreferencesToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`
		ALTER TABLE users DROP COLUMN IF EXISTS preferences;
	`)
	return err
}
