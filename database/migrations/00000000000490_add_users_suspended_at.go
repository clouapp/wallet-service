package migrations

// M00000000000490AddUsersSuspendedAt adds the platform suspension columns.
// sessions_revoked_at already exists on this branch, so this migration does
// not add it again. suspension_reason stays empty unless a later change
// decides a body for it.
type M00000000000490AddUsersSuspendedAt struct{}

func (r *M00000000000490AddUsersSuspendedAt) Signature() string {
	return "00000000000490_add_users_suspended_at"
}

func (r *M00000000000490AddUsersSuspendedAt) Up() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE users
			ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS suspension_reason TEXT`,
	)
	return err
}

func (r *M00000000000490AddUsersSuspendedAt) Down() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE users
			DROP COLUMN IF EXISTS suspension_reason,
			DROP COLUMN IF EXISTS suspended_at`,
	)
	return err
}
