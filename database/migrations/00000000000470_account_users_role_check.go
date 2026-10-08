package migrations

const accountUsersRoleCheck = "account_users_role_check"

type M00000000000470AccountUsersRoleCheck struct{}

func (r *M00000000000470AccountUsersRoleCheck) Signature() string {
	return "00000000000470_account_users_role_check"
}

func (r *M00000000000470AccountUsersRoleCheck) Up() error {
	if _, err := migrationQuery().Exec(`UPDATE account_users SET role = 'auditor' WHERE role = 'viewer'`); err != nil {
		return err
	}
	_, err := migrationQuery().Exec(
		`ALTER TABLE account_users ADD CONSTRAINT ` + accountUsersRoleCheck +
			` CHECK (role IN ('owner', 'admin', 'auditor', 'user'))`,
	)
	return err
}

func (r *M00000000000470AccountUsersRoleCheck) Down() error {
	_, err := migrationQuery().Exec(`ALTER TABLE account_users DROP CONSTRAINT IF EXISTS ` + accountUsersRoleCheck)
	return err
}
