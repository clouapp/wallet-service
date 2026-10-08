package migrations

// M00000000000570AccessTokensPermissionsJsonb stores access_tokens.permissions
// as jsonb. Appendix B item 450 names this column together with last_used_at
// and revoked_at; those timestamps already shipped as 350. The plan number
// 450 is 00000000000450_add_totp_last_used_counter, so this step is 570.
//
// A blank or null text value stays NULL: that is the omitted grant. A JSON
// array is kept. Any other text becomes an empty array, which grants nothing.
type M00000000000570AccessTokensPermissionsJsonb struct{}

func (r *M00000000000570AccessTokensPermissionsJsonb) Signature() string {
	return "00000000000570_access_tokens_permissions_jsonb"
}

func (r *M00000000000570AccessTokensPermissionsJsonb) Up() error {
	return execMigrationSQL(`DO $$
	DECLARE
		col_type text;
		rec record;
		parsed jsonb;
	BEGIN
		SELECT c.udt_name INTO col_type
		FROM information_schema.columns c
		WHERE c.table_schema = current_schema()
		  AND c.table_name = 'access_tokens'
		  AND c.column_name = 'permissions';
		IF col_type IS NULL THEN
			RAISE EXCEPTION 'access_tokens.permissions is missing';
		END IF;

		IF col_type <> 'jsonb' THEN
			ALTER TABLE access_tokens DROP COLUMN IF EXISTS permissions_jsonb;
			ALTER TABLE access_tokens ADD COLUMN permissions_jsonb jsonb;
			FOR rec IN SELECT id, permissions::text AS raw FROM access_tokens LOOP
				parsed := NULL;
				IF rec.raw IS NOT NULL AND btrim(rec.raw) <> '' THEN
					BEGIN
						parsed := btrim(rec.raw)::jsonb;
						IF jsonb_typeof(parsed) IS DISTINCT FROM 'array' THEN
							parsed := '[]'::jsonb;
						END IF;
					EXCEPTION
						WHEN invalid_text_representation THEN
							parsed := '[]'::jsonb;
					END;
				END IF;
				UPDATE access_tokens SET permissions_jsonb = parsed WHERE id = rec.id;
			END LOOP;
			ALTER TABLE access_tokens DROP COLUMN permissions;
			ALTER TABLE access_tokens RENAME COLUMN permissions_jsonb TO permissions;
		END IF;

		ALTER TABLE access_tokens DROP CONSTRAINT IF EXISTS access_tokens_permissions_array;
		ALTER TABLE access_tokens
			ADD CONSTRAINT access_tokens_permissions_array
			CHECK (permissions IS NULL OR jsonb_typeof(permissions) = 'array');
	END $$`)
}

func (r *M00000000000570AccessTokensPermissionsJsonb) Down() error {
	return execMigrationSQL(`DO $$
	DECLARE
		col_type text;
	BEGIN
		SELECT c.udt_name INTO col_type
		FROM information_schema.columns c
		WHERE c.table_schema = current_schema()
		  AND c.table_name = 'access_tokens'
		  AND c.column_name = 'permissions';
		IF col_type IS NULL THEN
			RAISE EXCEPTION 'access_tokens.permissions is missing';
		END IF;

		ALTER TABLE access_tokens DROP CONSTRAINT IF EXISTS access_tokens_permissions_array;
		IF col_type <> 'jsonb' THEN
			RETURN;
		END IF;

		ALTER TABLE access_tokens DROP COLUMN IF EXISTS permissions_text;
		ALTER TABLE access_tokens ADD COLUMN permissions_text text;
		UPDATE access_tokens SET permissions_text = permissions::text;
		ALTER TABLE access_tokens DROP COLUMN permissions;
		ALTER TABLE access_tokens RENAME COLUMN permissions_text TO permissions;
	END $$`)
}
