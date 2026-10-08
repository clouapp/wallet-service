package migrations

type M00000000000460AddSessionsRevokedAtToUsers struct{}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Signature() string {
	return "00000000000460_add_sessions_revoked_at_to_users"
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Up() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS sessions_revoked_at TIMESTAMPTZ`,
	)
	return err
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Down() error {
	_, err := migrationQuery().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS sessions_revoked_at`)
	return err
}
