package migrations

import "github.com/goravel/framework/contracts/database/schema"

// M00000000000720AddRefreshTokenSessionID gives each refresh token the id of
// its session (the "sid" claim of the access token). A refresh keeps it; rows
// from before are NULL and have no step-up window.
type M00000000000720AddRefreshTokenSessionID struct{}

func (r *M00000000000720AddRefreshTokenSessionID) Signature() string {
	return "00000000000720_add_refresh_token_session_id"
}

func (r *M00000000000720AddRefreshTokenSessionID) Up() error {
	if migrationSchema().HasColumn("refresh_tokens", "session_id") {
		return nil
	}
	return migrationSchema().Table("refresh_tokens", func(table schema.Blueprint) {
		table.Uuid("session_id").Nullable()
		table.Index("session_id")
	})
}

// Down drops the column; Postgres drops its index with it.
func (r *M00000000000720AddRefreshTokenSessionID) Down() error {
	if !migrationSchema().HasColumn("refresh_tokens", "session_id") {
		return nil
	}
	return migrationSchema().Table("refresh_tokens", func(table schema.Blueprint) {
		table.DropColumn("session_id")
	})
}
