package migrations

// M00000000000420CreateActivityLogTable is the slotkit activity trail.
// scope ” is the platform; account:{uuid} is one account. causer_type is the
// audience that acted. properties is a redacted before/after document: the
// column allowlist never selects a secret, a token hash, a spending limit, or
// an amount.
type M00000000000420CreateActivityLogTable struct{}

func (r *M00000000000420CreateActivityLogTable) Signature() string {
	return "00000000000420_create_activity_log_table"
}

func (r *M00000000000420CreateActivityLogTable) Up() error {
	stmts := []string{
		`CREATE TABLE activity_log (
			id            BIGSERIAL    NOT NULL,
			log_name      TEXT         NOT NULL DEFAULT 'default',
			scope         TEXT         NOT NULL DEFAULT '',
			event         TEXT         NOT NULL DEFAULT '',
			description   TEXT         NOT NULL DEFAULT '',
			subject_type  TEXT         NOT NULL DEFAULT '',
			subject_id    TEXT         NOT NULL DEFAULT '',
			causer_type   TEXT         NOT NULL DEFAULT '',
			causer_id     TEXT         NOT NULL DEFAULT '',
			causer_label  TEXT         NOT NULL DEFAULT '',
			properties    JSONB        NOT NULL DEFAULT '{}',
			batch_uuid    TEXT         NOT NULL DEFAULT '',
			created_at    TIMESTAMP(6) WITH TIME ZONE NOT NULL DEFAULT NOW(),
			CONSTRAINT activity_log_pkey PRIMARY KEY (id),
			CONSTRAINT activity_log_causer_type_known CHECK (
				causer_type IN ('', 'users', 'platform_admins', 'api_tokens', 'cli')
			),
			CONSTRAINT activity_log_properties_object CHECK (jsonb_typeof(properties) = 'object')
		)`,
		`CREATE INDEX activity_log_scope_created_idx ON activity_log (scope, created_at DESC)`,
		`CREATE INDEX activity_log_subject_idx ON activity_log (subject_type, subject_id, created_at DESC)`,
		`CREATE INDEX activity_log_causer_idx ON activity_log (causer_type, causer_id, created_at DESC)`,
		`CREATE INDEX activity_log_prune_idx ON activity_log (created_at)`,
	}
	for _, statement := range stmts {
		if _, err := migrationQuery().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000420CreateActivityLogTable) Down() error {
	_, err := migrationQuery().Exec(`DROP TABLE IF EXISTS activity_log`)
	return err
}
