package activitylog

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
)

// subjectSeparator joins a composite key into one subject id.
const subjectSeparator = ":"

// writeCapture is every After callback's body.
//
// The two guards at the top are the difference between a trail and a trail that
// lies, and neither is optional:
//
// db.Error — gorm's processor runs EVERY compiled callback unconditionally
// (callbacks.go:135-137); each built-in guards itself. Without this check the
// After hook fires when the write failed, when DryRun is on, and — worst — on
// the abort path that beforeMutation itself creates, writing an entry for a
// write that provably never happened.
//
// RowsAffected == 0 — two conditional UPDATEs on users exist precisely to match
// zero rows on the LOSING side of a second-factor activation race. Recording
// "Ana enabled the second factor" for the loser is a trail that lies, on the
// most sensitive table it covers.
func writeCapture(db *gorm.DB, event string, after []map[string]any) {
	if db.Error != nil || db.RowsAffected == 0 {
		return
	}

	value, ok := db.InstanceGet(stateKey)
	if !ok {
		return
	}
	state, ok := value.(*captureState)
	if !ok {
		return
	}

	// A create has no before-image; its after-image is the reflected dest, read
	// only once the keys gorm fills in are there.
	if event == "created" {
		state.keys = keyColumns(db, state.table)
		after = reflectImage(db, state)
	}

	entry := buildEntry(db, state, event, after)

	if err := writeEntry(db.Statement.Context, db.Statement.ConnPool, entry); err != nil {
		_ = db.AddError(err)
	}
}

// buildEntry assembles one entry for one STATEMENT.
//
// One entry per statement, never per row: gorm batches a slice Create into a
// single INSERT and therefore a single callback, and a predicated DELETE is one
// statement over N rows. A design that emitted one entry per affected row would
// be waiting for callbacks that never fire.
func buildEntry(db *gorm.DB, state *captureState, event string, after []map[string]any) Entry {
	ctx := db.Statement.Context
	tbl := state.table

	causer, _ := CauserFromContext(ctx)

	entry := Entry{
		LogName:     tbl.LogName,
		Scope:       ScopeFromContext(ctx),
		Event:       event,
		SubjectType: tbl.Subject,
		CauserType:  causer.Type,
		CauserID:    causer.ID,
		CauserLabel: causer.Label,
		BatchUUID:   BatchFromContext(ctx),
		Properties:  map[string]any{},
	}

	// Intent overrides the mechanical event. A callback sees a table, columns
	// and a WHERE — it cannot see that a users UPDATE is a sign-in.
	if intent, ok := takeIntent(ctx); ok {
		if intent.Event != "" {
			entry.Event = intent.Event
		}
		entry.Description = intent.Description
	}

	ids := subjectIDs(state.before, state.keys)
	if len(ids) == 0 {
		ids = subjectIDs(after, state.keys)
	}

	before := images(tbl, state.before, state.flags)
	newImages := images(tbl, after, state.flags)
	for k, v := range state.flags {
		entry.Properties[k] = v
	}

	switch {
	case len(ids) == 1:
		entry.SubjectID = ids[0]
		if len(before) == 1 {
			entry.Properties["old"] = before[0]
		}
		if len(newImages) == 1 {
			entry.Properties["new"] = newImages[0]
		}
	default:
		// Several rows, or none identifiable. SubjectID stays empty rather than
		// naming one of them, which would read as "this row changed" and be
		// wrong about the others.
		if len(ids) > 0 {
			entry.Properties["subjectIds"] = ids
		}
		entry.Properties["rows"] = db.RowsAffected
		if len(before) > 0 {
			entry.Properties["oldRows"] = before
		}
		if len(newImages) > 0 {
			entry.Properties["newRows"] = newImages
		}
	}

	return entry
}

// destImage builds what each affected row will LOOK LIKE, by overlaying the
// statement's assignments onto the row that was there.
//
// Overlay, not the assignment map alone, and both halves of that matter:
//
// A partial UPDATE assigns only the columns it changes. Recording the map by
// itself makes every untouched column read as REMOVED — a rename of a publisher
// showed `status: active -> —`, which is a diff that lies about a column the
// statement never mentioned.
//
// And a jsonb redactor decides from the ROW. `settings` is redacted by its
// `category`, which a payload-only UPDATE does not assign; without the row
// underneath, the rule sees no category, correctly refuses to guess, and prunes
// the whole document — so the after-image of a settings save came out empty on
// exactly the screen the redactor exists for.
//
// The after half of the redaction stays weaker than the before half, and
// honestly so: the before-image SELECT never NAMES a credential column, while
// this filters one out of a map the process already holds. The user
// repository's column-scoped writers still assign credential columns —
// SetPassword sends `password`, StoreTotpSecret sends `totp_secret` — and
// overlay merges that assignment map onto the before-image, so the column
// allowlist (Table.image) is the only thing between the hash and this table.
// It is load-bearing; do not prune it because no full-row writer exists.
func destImage(db *gorm.DB) []map[string]any {
	value, ok := db.InstanceGet(stateKey)
	if !ok {
		return nil
	}
	state, ok := value.(*captureState)
	if !ok {
		return nil
	}

	assignments, ok := db.Statement.Dest.(map[string]any)
	if !ok {
		return nil
	}

	// One after-image per affected row: the statement assigns the same values to
	// all of them, but each starts from its own before-image.
	if len(state.before) == 0 {
		return []map[string]any{overlay(nil, assignments)}
	}

	out := make([]map[string]any, 0, len(state.before))
	for _, before := range state.before {
		out = append(out, overlay(before, assignments))
	}

	return out
}

// overlay copies base and applies the assignments on top.
func overlay(base, assignments map[string]any) map[string]any {
	row := make(map[string]any, len(base)+len(assignments))
	for k, v := range base {
		row[k] = v
	}
	for k, v := range assignments {
		row[k] = v
	}

	return row
}

// images applies the allowlist and the jsonb redaction to raw rows, merging any
// flags the redactor raised.
func images(tbl Table, rows []map[string]any, flags map[string]any) []map[string]any {
	if len(rows) == 0 {
		return nil
	}

	out := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		img, raised := tbl.image(raw)
		for k, v := range raised {
			flags[k] = v
		}
		out = append(out, img)
	}

	return out
}

// reflectImage reads a create's values off the reflected destination, after gorm
// has filled the primary keys in.
func reflectImage(db *gorm.DB, state *captureState) []map[string]any {
	schema := db.Statement.Schema
	if schema == nil {
		return nil
	}

	wanted := make(map[string]bool, len(state.table.Columns)+len(state.keys))
	for _, c := range state.table.Columns {
		wanted[c] = true
	}
	for _, c := range state.keys {
		wanted[c] = true
	}

	// Raw rows, like the before-image's: the allowlist is applied when the entry
	// is built, so a key column that is not recordable can still identify the
	// row.
	read := func(v reflect.Value) map[string]any {
		row := map[string]any{}
		for _, field := range schema.Fields {
			if !wanted[field.DBName] {
				continue
			}
			if value, zero := field.ValueOf(db.Statement.Context, v); !zero || value != nil {
				row[field.DBName] = value
			}
		}
		return row
	}

	rv := db.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]map[string]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, read(rv.Index(i)))
		}
		return out
	case reflect.Struct:
		return []map[string]any{read(rv)}
	default:
		return nil
	}
}

// keyColumns names the columns that identify a row in this table.
//
// The registration wins, because a primary key is neither always one column nor
// always called "id": publisher_games is keyed by (publisher_id, game_id) and has no
// id at all. Falling back to "id" for such a table would fail the before-image
// SELECT, and a failed before-image ABORTS THE WRITE — so getting this wrong
// does not degrade the trail, it takes the feature down.
func keyColumns(db *gorm.DB, tbl Table) []string {
	if len(tbl.KeyColumns) > 0 {
		return tbl.KeyColumns
	}
	if s := db.Statement.Schema; s != nil && len(s.PrimaryFieldDBNames) > 0 {
		return s.PrimaryFieldDBNames
	}
	return []string{"id"}
}

// scanRows turns a *sql.Rows into plain maps.
func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []map[string]any
	for rows.Next() {
		cells := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(columns))
		for i, name := range columns {
			row[name] = normalise(cells[i])
		}
		out = append(out, row)
	}

	return out, rows.Err()
}

// normalise turns driver []byte into a string so the entry's JSON reads as text
// rather than as a base64 blob.
func normalise(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// subjectIDs renders each row's key as one string, joining a composite key.
func subjectIDs(rows []map[string]any, keys []string) []string {
	if len(keys) == 0 {
		return nil
	}

	var ids []string
	for _, row := range rows {
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			v, ok := row[k]
			if !ok || v == nil {
				parts = nil
				break
			}
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		if len(parts) == 0 {
			continue
		}
		ids = append(ids, strings.Join(parts, subjectSeparator))
	}

	return ids
}
