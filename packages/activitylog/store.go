package activitylog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// The size caps for one entry's document. The column allowlist bounds how many
// columns are recorded; nothing else bounds how wide they are, and an unbounded
// jsonb times a year of retention times four indexes is a table size decided by
// omission. See docs/specs/2026-08-27-activity-log-design.md §8.
const (
	maxValueBytes    = 4 << 10
	maxDocumentBytes = 16 << 10
)

// ExecPool is the narrow slice of gorm's ConnPool this package writes through.
//
// It is declared here rather than imported for one reason worth stating: it
// makes the mistake unexpressible. Writing the entry through a *gorm.DB would
// re-enter the create processor and fire this plugin's own callbacks for
// activity_log (§5.4), mutate the live statement the outer callback is still
// assembling (§5.6), and — inside a transaction — take a second connection from
// the pool and block on the row that transaction has already locked (§5.3).
// A type that cannot reach a *gorm.DB cannot make any of those three.
type ExecPool interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const insertEntry = `INSERT INTO activity_log
	(log_name, scope, event, description, subject_type, subject_id,
	 causer_type, causer_id, causer_label, properties, batch_uuid, created_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

// writeEntry appends one entry through the pool it is handed.
//
// Handed, never resolved: inside a transaction that pool IS the *sql.Tx, which
// is what makes the entry atomic with the act it describes — a rollback takes
// the entry with it, and there is no window in which the act is committed and
// the trail is not.
func writeEntry(ctx context.Context, pool ExecPool, e Entry) error {
	props, err := json.Marshal(capProperties(e.Properties))
	if err != nil {
		return fmt.Errorf("activitylog: marshal properties: %w", err)
	}

	logName := e.LogName
	if logName == "" {
		logName = "default"
	}
	created := e.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}

	if _, err := pool.ExecContext(ctx, insertEntry,
		logName, e.Scope, e.Event, e.Description, e.SubjectType, e.SubjectID,
		e.CauserType, e.CauserID, e.CauserLabel, string(props), e.BatchUUID, created,
	); err != nil {
		return fmt.Errorf("activitylog: insert entry: %w", err)
	}

	return nil
}

// capProperties bounds one entry's document and MARKS the result rather than
// quietly shrinking it. A trail that silently drops the interesting half of a
// diff is worse than one that says it did: the reader of a truncated entry knows
// to go looking, the reader of a quietly trimmed one does not.
func capProperties(p map[string]any) map[string]any {
	if len(p) == 0 {
		return map[string]any{}
	}

	out := make(map[string]any, len(p)+1)
	truncated := false

	for k, v := range p {
		if s, ok := v.(string); ok && len(s) > maxValueBytes {
			out[k] = s[:maxValueBytes]
			truncated = true
			continue
		}
		out[k] = v
	}

	// A document can blow the budget while every individual value is inside its
	// own cap — many columns, each legal. Metadata survives and the images do
	// not: which record and who touched it is what the trail is FOR, and the
	// diff is the detail.
	if encoded, err := json.Marshal(out); err != nil || len(encoded) > maxDocumentBytes {
		out = map[string]any{"oversize": true}
		if err == nil {
			out["bytes"] = len(encoded)
		}
		truncated = true
	}

	if truncated {
		out["truncated"] = true
	}

	return out
}
