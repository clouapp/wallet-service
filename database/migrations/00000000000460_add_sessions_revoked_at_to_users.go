package migrations

import (
	appfacades "github.com/macrowallets/waas/app/facades"
)

// M00000000000460AddSessionsRevokedAtToUsers adds the session watermark: a
// dashboard session issued before it is refused, which is how changing or
// resetting the password and disabling TOTP end every open session.
type M00000000000460AddSessionsRevokedAtToUsers struct{}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Signature() string {
	return "00000000000460_add_sessions_revoked_at_to_users"
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Up() error {
	_, err := appfacades.Orm().Query().Exec(
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS sessions_revoked_at TIMESTAMPTZ`,
	)
	return err
}

func (r *M00000000000460AddSessionsRevokedAtToUsers) Down() error {
	_, err := appfacades.Orm().Query().Exec(`ALTER TABLE users DROP COLUMN IF EXISTS sessions_revoked_at`)
	return err
}
