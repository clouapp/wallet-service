package migrations

// M00000000000500CreateMfaCredentialsTable is the shared TOTP secret store
// from S3.4.5: subject_type is users or platform_admins, and last_used_counter
// is the replay step.
type M00000000000500CreateMfaCredentialsTable struct{}

func (r *M00000000000500CreateMfaCredentialsTable) Signature() string {
	return "00000000000500_create_mfa_credentials_table"
}

func (r *M00000000000500CreateMfaCredentialsTable) Up() error {
	_, err := migrationQuery().Exec(`
		CREATE TABLE IF NOT EXISTS mfa_credentials (
			id                 UUID         NOT NULL,
			subject_type       VARCHAR(32)  NOT NULL,
			subject_id         UUID         NOT NULL,
			secret             TEXT         NOT NULL DEFAULT '',
			confirmed_at       TIMESTAMPTZ,
			last_used_counter  BIGINT       NOT NULL DEFAULT 0,
			created_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			updated_at         TIMESTAMP(0) WITHOUT TIME ZONE,
			CONSTRAINT mfa_credentials_pkey PRIMARY KEY (id),
			CONSTRAINT mfa_credentials_subject_type_check CHECK (subject_type IN ('users', 'platform_admins')),
			CONSTRAINT mfa_credentials_subject_unique UNIQUE (subject_type, subject_id)
		)`)
	return err
}

func (r *M00000000000500CreateMfaCredentialsTable) Down() error {
	_, err := migrationQuery().Exec(`DROP TABLE IF EXISTS mfa_credentials`)
	return err
}
