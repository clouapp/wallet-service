package migrations

import "github.com/goravel/framework/facades"

// M00000000000300AccountUsersRoleCheck collapses the retired "viewer" label
// onto auditor and refuses any other account role at the database.
type M00000000000300AccountUsersRoleCheck struct{}

const accountUsersRoleCheck = "account_users_role_check"

func (r *M00000000000300AccountUsersRoleCheck) Signature() string {
	return "00000000000300_account_users_role_check"
}

func (r *M00000000000300AccountUsersRoleCheck) Up() error {
	if _, err := facades.Orm().Query().Exec(`UPDATE account_users SET role = 'auditor' WHERE role = 'viewer'`); err != nil {
		return err
	}
	_, err := facades.Orm().Query().Exec(
		`ALTER TABLE account_users ADD CONSTRAINT ` + accountUsersRoleCheck +
			` CHECK (role IN ('owner', 'admin', 'auditor', 'user'))`,
	)
	return err
}

func (r *M00000000000300AccountUsersRoleCheck) Down() error {
	_, err := facades.Orm().Query().Exec(`ALTER TABLE account_users DROP CONSTRAINT IF EXISTS ` + accountUsersRoleCheck)
	return err
}
