package activitylog

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// trailTable is this package's own table. Nothing may register it, and the
// plugin refuses it at capture time too — see the doc on Register.
const trailTable = "activity_log"

// defaultSetKeySuffix mirrors the convention a redacted read already uses in
// this kind of surface: the value is gone and "<field>Set" answers whether it
// was there.
const defaultSetKeySuffix = "Set"

// JSONRule redacts one jsonb column.
//
// RedactFor answers TWO things, and collapsing them into one is a leak in
// either direction. An empty path list is the honest answer for a document that
// holds no secret, so it cannot also mean "I do not recognise this row" —
// reading it that way prunes documents worth keeping. And a row the rule does
// not recognise cannot fall through to "nothing to redact", because a newly
// added document shape is exactly the change that would otherwise publish its
// secrets on the day it ships: the shape is added, the rule has no branch for
// it, and nothing fails.
//
// So: paths says WHAT to prune, known says whether the rule understood the row
// at all. known=false prunes everything and flags the entry.
type JSONRule struct {
	RedactFor func(row map[string]any) (paths []string, known bool)
	// SetKeySuffix overrides "Set" for the boolean that replaces a pruned path.
	SetKeySuffix string
}

// Table is one audited table's registration.
//
// Everything application-specific about auditing arrives through this struct.
// The package cannot import the host application (tests/architecture pins an
// empty allow-set for packages/), which is what keeps it portable — and what
// forces the redaction rules to be data rather than code that knows about
// settings categories or user credentials.
type Table struct {
	// Name is the SQL table name, as the statement reports it.
	Name string
	// LogName is the channel the entries land in. Empty means "default".
	LogName string
	// Subject is what an entry calls this table's rows ("publisher", "user").
	Subject string
	// Columns is the ALLOWLIST, and it is the redaction: the before-image
	// SELECT names these columns and no others, so an omitted credential is
	// never read rather than read and filtered.
	Columns []string
	// JSONPaths redacts inside a jsonb column, which a column allowlist cannot
	// reach into.
	JSONPaths map[string]JSONRule
	// KeyColumns identifies a row, overriding the model's primary key.
	//
	// It exists because a primary key is not always one column and is not always
	// called "id". publisher_games is keyed by (publisher_id, game_id) and
	// publisher_role_permissions by (publisher_id, role_id, permission_id); neither
	// has an `id` column at all, so a lookup that assumed one would fail the
	// before-image SELECT — and, because a failed before-image aborts the write,
	// would take the whole feature down rather than just the trail.
	//
	// Empty means "ask the model", which is right for every table that has a
	// simple primary key.
	KeyColumns []string
}

var reg = struct {
	mu     sync.RWMutex
	tables map[string]Table
}{tables: map[string]Table{}}

// Register declares a table auditable. It is called from the host application's
// service provider, once per table, at boot.
//
// It refuses three things, each because the silent version is worse than a
// failed boot: an unnamed table (a registration that can never match), a
// duplicate (a second registration would swap the allowlist out from under the
// first, and the surviving one would be whichever provider ran last), and
// activity_log itself (an entry that produces an entry does not produce a wrong
// row, it produces a process that never returns).
func Register(t Table) error {
	if t.Name == "" {
		return fmt.Errorf("activitylog: register: table name is required")
	}
	if t.Name == trailTable {
		return fmt.Errorf("activitylog: register: %s is the trail itself and cannot be audited", trailTable)
	}

	reg.mu.Lock()
	defer reg.mu.Unlock()

	if _, exists := reg.tables[t.Name]; exists {
		return fmt.Errorf("activitylog: register: %s is already registered", t.Name)
	}
	reg.tables[t.Name] = t

	return nil
}

// lookup returns a table's registration.
func lookup(name string) (Table, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	t, ok := reg.tables[name]
	return t, ok
}

// RegisteredTables lists what is audited, sorted.
//
// Exported for the host application's guard test, which reconciles it against
// that application's own canonical list of console-mutable tables — so adding a
// write endpoint forces touching both, and a table nobody registered fails CI
// rather than going quietly unaudited.
func RegisteredTables() []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	names := make([]string, 0, len(reg.tables))
	for name := range reg.tables {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// resetRegistry clears the registry. Tests only.
func resetRegistry() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.tables = map[string]Table{}
}

// selectColumns returns the columns the before-image SELECT should name.
func (t Table) selectColumns() []string {
	out := make([]string, len(t.Columns))
	copy(out, t.Columns)
	return out
}

// image reduces a row to what may be recorded, and reports what it had to do.
//
// The returned flags land on the entry's properties rather than in a log line:
// a redaction that had to fall back needs to be visible to whoever reads the
// entry later, not to whoever was tailing stderr at the time.
func (t Table) image(row map[string]any) (map[string]any, map[string]any) {
	img := make(map[string]any, len(t.Columns))
	flags := map[string]any{}

	allowed := make(map[string]bool, len(t.Columns))
	for _, c := range t.Columns {
		allowed[c] = true
	}

	for column, value := range row {
		if !allowed[column] {
			continue
		}

		rule, isJSON := t.JSONPaths[column]
		if !isJSON {
			img[column] = value
			continue
		}

		doc, ok := decodeJSONColumn(value)
		if !ok {
			// Passing the raw value through would put unredacted bytes into a
			// table one permission can read.
			img[column] = map[string]any{}
			flags["redactorUnparseable"] = true
			continue
		}

		img[column] = rule.apply(doc, row, flags)
	}

	return img, flags
}

// apply prunes one decoded document.
func (r JSONRule) apply(doc, row map[string]any, flags map[string]any) map[string]any {
	if r.RedactFor == nil {
		// No rule at all is the same posture as a rule that does not recognise
		// the row: prune everything.
		flags["redactorUnknownCategory"] = true
		return map[string]any{}
	}

	paths, known := r.RedactFor(row)
	if !known {
		flags["redactorUnknownCategory"] = true
		return map[string]any{}
	}

	suffix := r.SetKeySuffix
	if suffix == "" {
		suffix = defaultSetKeySuffix
	}

	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	for _, path := range paths {
		_, present := out[path]
		delete(out, path)
		out[path+suffix] = present
	}

	return out
}

// decodeJSONColumn accepts what database/sql hands back for a jsonb column,
// which is []byte as often as string depending on the driver and the path.
func decodeJSONColumn(value any) (map[string]any, bool) {
	var raw []byte
	switch v := value.(type) {
	case nil:
		return map[string]any{}, true
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	case map[string]any:
		return v, true
	default:
		return nil, false
	}

	if len(raw) == 0 {
		return map[string]any{}, true
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}

	return doc, true
}
