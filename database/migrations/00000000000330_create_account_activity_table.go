package migrations

import "github.com/goravel/framework/facades"

// M00000000000330CreateAccountActivityTable is the account audit trail.
// account_id is nullable so a platform feature write can be stored without
// an account. The account list query only returns rows for one account.
// metadata is a small JSON object of names and booleans, never a secret.
type M00000000000330CreateAccountActivityTable struct{}

func (r *M00000000000330CreateAccountActivityTable) Signature() string {
	return "00000000000330_create_account_activity_table"
}

func (r *M00000000000330CreateAccountActivityTable) Up() error {
	stmts := []string{
		`CREATE TABLE account_activity (
			id            UUID         NOT NULL,
			account_id    UUID,
			actor_user_id UUID         NOT NULL,
			action        TEXT         NOT NULL,
			target_type   TEXT         NOT NULL,
			target_id     TEXT         NOT NULL,
			metadata      JSONB        NOT NULL,
			created_at    TIMESTAMP(6) WITH TIME ZONE NOT NULL,
			CONSTRAINT account_activity_pkey PRIMARY KEY (id),
			CONSTRAINT account_activity_account_id_foreign FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
			CONSTRAINT account_activity_actor_user_id_foreign FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE RESTRICT,
			CONSTRAINT account_activity_action_not_blank CHECK (length(btrim(action)) > 0),
			CONSTRAINT account_activity_target_type_not_blank CHECK (length(btrim(target_type)) > 0),
			CONSTRAINT account_activity_target_id_not_blank CHECK (length(btrim(target_id)) > 0),
			CONSTRAINT account_activity_metadata_object CHECK (jsonb_typeof(metadata) = 'object')
		)`,
		`CREATE INDEX account_activity_account_created_idx ON account_activity (account_id, created_at DESC, id DESC) WHERE account_id IS NOT NULL`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000330CreateAccountActivityTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS account_activity`)
	return err
}
