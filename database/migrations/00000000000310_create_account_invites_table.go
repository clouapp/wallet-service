package migrations

import "github.com/goravel/framework/facades"

type M00000000000310CreateAccountInvitesTable struct{}

func (r *M00000000000310CreateAccountInvitesTable) Signature() string {
	return "00000000000310_create_account_invites_table"
}

func (r *M00000000000310CreateAccountInvitesTable) Up() error {
	stmts := []string{
		`CREATE TABLE account_invites (
			id          UUID         NOT NULL,
			account_id  UUID         NOT NULL,
			email       VARCHAR(255) NOT NULL,
			role        VARCHAR(20)  NOT NULL,
			token_hash  TEXT         NOT NULL,
			invited_by  UUID,
			expires_at  TIMESTAMPTZ  NOT NULL,
			accepted_at TIMESTAMPTZ,
			revoked_at  TIMESTAMPTZ,
			created_at  TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at  TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT account_invites_pkey PRIMARY KEY (id),
			CONSTRAINT account_invites_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
			CONSTRAINT account_invites_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES users(id),
			CONSTRAINT account_invites_role_check CHECK (role IN ('owner', 'admin', 'auditor', 'user'))
		)`,
		`CREATE UNIQUE INDEX account_invites_pending_email ON account_invites (account_id, lower(email)) WHERE accepted_at IS NULL AND revoked_at IS NULL`,
		`CREATE INDEX account_invites_token_hash ON account_invites (token_hash)`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000310CreateAccountInvitesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS account_invites`)
	return err
}
