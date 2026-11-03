package drivers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

// Executor is the subset of database/sql the Database needs. *sql.DB,
// *sql.Tx and *sql.Conn all satisfy it; the DriverCreator hands the store the
// *sql.DB behind the ORM's default connection.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Database persists feature values in a SQL table: one row per
// (feature, scope), value held as JSON. Unlike the memory and Redis stores it
// survives a restart and is auditable from SQL — the value column is jsonb, so
// `WHERE value->>'tier' = 'gold'` works.
//
// The statements target PostgreSQL. `ON CONFLICT` is what makes SetIfAbsent a
// single atomic round-trip, which the manager's memoization depends on: a read
// that resolves must never clobber a concurrent explicit override. SQLite
// (>= 3.24) and CockroachDB accept the same syntax; MySQL would need
// `ON DUPLICATE KEY UPDATE` and a variant of this file.
//
// The table must carry a unique constraint on (name, scope) — without it
// `ON CONFLICT` has no arbiter and every write errors. The migration the setup
// publishes creates it.
type Database struct {
	db    Executor
	table string
}

// defaultTable is the table a Database uses when none is named. It is
// the one the published migration creates.
const defaultTable = "features"

// NewDatabase returns a Database over table. An empty table defaults
// to "features". The table must already exist; the migration the setup
// publishes creates it.
func NewDatabase(db Executor, table string) *Database {
	if table == "" {
		table = defaultTable
	}
	return &Database{db: db, table: quoteIdentifier(table)}
}

func (s *Database) Get(ctx context.Context, feature, scopeKey string) (any, bool, error) {
	// value::text keeps the driver out of the decision: every driver reads a
	// text column as a string, whereas a jsonb column round-trips differently
	// between pgx and lib/pq. encoding/json does the decoding either way.
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value::text FROM `+s.table+` WHERE name = $1 AND scope = $2`,
		feature, scopeKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("featureflag: database select: %w", err)
	}
	v, err := decodeValue([]byte(raw))
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *Database) Set(ctx context.Context, feature, scopeKey string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	// $3::text::jsonb: the value is bound as text and cast in the database, so
	// the JSON is never re-encoded by a driver's jsonb codec.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO `+s.table+` (name, scope, value, created_at, updated_at)
		 VALUES ($1, $2, $3::text::jsonb, NOW(), NOW())
		 ON CONFLICT (name, scope) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		feature, scopeKey, string(raw),
	); err != nil {
		return fmt.Errorf("featureflag: database upsert: %w", err)
	}
	return nil
}

func (s *Database) SetIfAbsent(ctx context.Context, feature, scopeKey string, value any) (bool, error) {
	raw, err := encodeValue(value)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO `+s.table+` (name, scope, value, created_at, updated_at)
		 VALUES ($1, $2, $3::text::jsonb, NOW(), NOW())
		 ON CONFLICT (name, scope) DO NOTHING`,
		feature, scopeKey, string(raw),
	)
	if err != nil {
		return false, fmt.Errorf("featureflag: database insert: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("featureflag: database rows affected: %w", err)
	}
	return affected > 0, nil
}

func (s *Database) SetForAllScopes(ctx context.Context, feature string, value any) error {
	raw, err := encodeValue(value)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE `+s.table+` SET value = $1::text::jsonb, updated_at = NOW() WHERE name = $2`,
		string(raw), feature,
	); err != nil {
		return fmt.Errorf("featureflag: database update: %w", err)
	}
	return nil
}

func (s *Database) Delete(ctx context.Context, feature, scopeKey string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM `+s.table+` WHERE name = $1 AND scope = $2`,
		feature, scopeKey,
	); err != nil {
		return fmt.Errorf("featureflag: database delete: %w", err)
	}
	return nil
}

func (s *Database) Purge(ctx context.Context, features []string) error {
	if len(features) == 0 {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+s.table); err != nil {
			return fmt.Errorf("featureflag: database purge: %w", err)
		}
		return nil
	}
	// An IN list of numbered placeholders rather than `= ANY($1)`: array
	// binding is driver-specific, this is not.
	placeholders := make([]string, len(features))
	args := make([]any, len(features))
	for i, feature := range features {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = feature
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM `+s.table+` WHERE name IN (`+strings.Join(placeholders, ", ")+`)`,
		args...,
	); err != nil {
		return fmt.Errorf("featureflag: database purge: %w", err)
	}
	return nil
}

// quoteIdentifier makes a table name safe to interpolate. A table name cannot
// be a bind parameter, and this one arrives from configuration.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var _ contracts.Driver = (*Database)(nil)
