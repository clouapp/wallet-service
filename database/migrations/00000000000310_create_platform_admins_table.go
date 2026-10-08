package migrations

import "github.com/goravel/framework/facades"

// M00000000000310CreatePlatformAdminsTable records which dashboard users may
// manage platform-wide feature flags. The row is a user id, and that id is
// unique: one membership per user. It stores no password, no secret, and no
// role. Provisioning stays outside this slice.
type M00000000000310CreatePlatformAdminsTable struct{}

func (r *M00000000000310CreatePlatformAdminsTable) Signature() string {
	return "00000000000310_create_platform_admins_table"
}

func (r *M00000000000310CreatePlatformAdminsTable) Up() error {
	statement := `CREATE TABLE platform_admins (
		user_id    UUID NOT NULL,
		created_at TIMESTAMP(6) WITH TIME ZONE,
		updated_at TIMESTAMP(6) WITH TIME ZONE,
		CONSTRAINT platform_admins_pkey PRIMARY KEY (user_id),
		CONSTRAINT platform_admins_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	)`
	if _, err := facades.Orm().Query().Exec(statement); err != nil {
		return err
	}
	return nil
}

func (r *M00000000000310CreatePlatformAdminsTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS platform_admins`)
	return err
}
