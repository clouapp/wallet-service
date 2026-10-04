package migrations

import "github.com/goravel/framework/facades"

// M00000000000320CreateGlobalFeaturesTable stores one boolean per flag key
// for the whole platform. The catalog names the keys and the default a
// missing row uses. This migration does not seed flags. The value is a
// boolean, never a secret. An account flag stays in features.
type M00000000000320CreateGlobalFeaturesTable struct{}

func (r *M00000000000320CreateGlobalFeaturesTable) Signature() string {
	return "00000000000320_create_global_features_table"
}

func (r *M00000000000320CreateGlobalFeaturesTable) Up() error {
	statement := `CREATE TABLE global_features (
		id         BIGSERIAL    NOT NULL,
		"key"      TEXT         NOT NULL,
		enabled    BOOLEAN      NOT NULL,
		created_at TIMESTAMP(6) WITH TIME ZONE,
		updated_at TIMESTAMP(6) WITH TIME ZONE,
		CONSTRAINT global_features_pkey PRIMARY KEY (id),
		CONSTRAINT global_features_key_unique UNIQUE ("key"),
		CONSTRAINT global_features_key_not_blank CHECK (length(btrim("key")) > 0)
	)`
	if _, err := facades.Orm().Query().Exec(statement); err != nil {
		return err
	}
	return nil
}

func (r *M00000000000320CreateGlobalFeaturesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS global_features`)
	return err
}
