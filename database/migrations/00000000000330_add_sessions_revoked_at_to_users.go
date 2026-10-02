package migrations

import "github.com/goravel/framework/facades"

// M00000000000330AddSessionsRevokedAtToUsers adds the session watermark: a
// dashboard session issued before it is refused, which is how changing or
// resetting the password and disabling TOTP end every open session.
type M00000000000330AddSessionsRevokedAtToUsers struct{}

func (r *M00000000000330AddSessionsRevokedAtToUsers) Signature() string {
	return "00000000000330_add_sessions_revoked_at_to_users"
}

func (r *M00000000000330AddSessionsRevokedAtToUsers) Up() error {
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS sessions_revoked_at TIMESTAMPTZ`,
	)
	return err
}

func (r *M00000000000330AddSessionsRevokedAtToUsers) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS sessions_revoked_at`)
	return err
}
