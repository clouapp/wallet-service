package migrations

import "github.com/goravel/framework/contracts/database/orm"

// M00000000000560AddAccountInvitesWalletRoles adds the nullable jsonb column
// S3.4.3 names on account_invites. The table was created as 480, so the plan
// number 400 is not reused. Rows that already exist stay NULL: there is no
// source to backfill.
type M00000000000560AddAccountInvitesWalletRoles struct{}

func (r *M00000000000560AddAccountInvitesWalletRoles) Signature() string {
	return "00000000000560_add_account_invites_wallet_roles"
}

func (r *M00000000000560AddAccountInvitesWalletRoles) Up() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE account_invites ADD COLUMN IF NOT EXISTS wallet_roles JSONB`,
	)
	return err
}

func (r *M00000000000560AddAccountInvitesWalletRoles) Down() error {
	_, err := migrationQuery().Exec(
		`ALTER TABLE account_invites DROP COLUMN IF EXISTS wallet_roles`,
	)
	return err
}

func relationPresent(tx orm.Query, query string) (bool, error) {
	n, err := countQuery(tx, query)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
