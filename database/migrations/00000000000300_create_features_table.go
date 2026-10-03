package migrations

import "github.com/goravel/framework/facades"

// M00000000000300CreateFeaturesTable stores one boolean per (account, key).
// The catalog in app/services/features names the keys. A missing row is
// disabled, so this migration does not seed flags. The value is a boolean,
// never a secret.
type M00000000000300CreateFeaturesTable struct{}

func (r *M00000000000300CreateFeaturesTable) Signature() string {
	return "00000000000300_create_features_table"
}

func (r *M00000000000300CreateFeaturesTable) Up() error {
	stmts := []string{
		`CREATE TABLE features (
			id         BIGSERIAL    NOT NULL,
			account_id UUID         NOT NULL,
			"key"      TEXT         NOT NULL,
			enabled    BOOLEAN      NOT NULL,
			created_at TIMESTAMP(6) WITH TIME ZONE,
			updated_at TIMESTAMP(6) WITH TIME ZONE,
			CONSTRAINT features_pkey PRIMARY KEY (id),
			CONSTRAINT features_account_id_foreign FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
			CONSTRAINT features_account_key_unique UNIQUE (account_id, "key"),
			CONSTRAINT features_key_not_blank CHECK (length(btrim("key")) > 0)
		)`,
	}
	for _, statement := range stmts {
		if _, err := facades.Orm().Query().Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *M00000000000300CreateFeaturesTable) Down() error {
	_, err := facades.Orm().Query().Exec(`DROP TABLE IF EXISTS features`)
	return err
}
