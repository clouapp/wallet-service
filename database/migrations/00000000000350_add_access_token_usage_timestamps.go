package migrations

// M00000000000350AddAccessTokenUsageTimestamps adds the S3.4.6 audit
// timestamps. last_used_at is stamped after a successful API-token
// authentication. revoked_at is a soft revoke: the row stays for audit.
type M00000000000350AddAccessTokenUsageTimestamps struct{}

func (r *M00000000000350AddAccessTokenUsageTimestamps) Signature() string {
	return "00000000000350_add_access_token_usage_timestamps"
}

func (r *M00000000000350AddAccessTokenUsageTimestamps) Up() error {
	if err := execMigrationSQL(`ALTER TABLE access_tokens ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ`); err != nil {
		return err
	}
	return execMigrationSQL(`ALTER TABLE access_tokens ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ`)
}

func (r *M00000000000350AddAccessTokenUsageTimestamps) Down() error {
	if err := execMigrationSQL(`ALTER TABLE access_tokens DROP COLUMN IF EXISTS last_used_at`); err != nil {
		return err
	}
	return execMigrationSQL(`ALTER TABLE access_tokens DROP COLUMN IF EXISTS revoked_at`)
}
