package activitylog

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pluginName is the gorm plugin name. gorm refuses a duplicate registration by
// name (gorm.go:533-544 returns ErrRegistered), which the service provider turns
// into boot idempotence rather than a fatal second install.
const pluginName = "activitylog"

// stateKey carries the before-image from the Before callback to the After one.
// It goes through InstanceSet/InstanceGet, which scopes the value to THIS
// statement — a package-level variable would hand one request's before-image to
// another's entry under concurrency.
const stateKey = ":activitylog:state"

// captureState is what a Before callback leaves for its After.
type captureState struct {
	table Table
	// keys identifies a row in this table. Resolved once, in the Before hook,
	// because the After hook has no statement left to ask.
	keys []string
	// before holds the RAW selected rows: the allowlist is applied when the
	// entry is built, so the key columns can identify the row even when they
	// are not themselves recordable.
	before []map[string]any
	flags  map[string]any
}

// plugin is the automatic capture, registered once at boot on the *gorm.DB the
// framework caches per connection.
//
// This is the seam the framework itself uses: goravel/framework's
// database/driver/gorm.go registers its telemetry plugin and dbresolver on the
// same instance. Because gorm keeps `callbacks` on Config and DB embeds *Config,
// one registration covers every query, every WithContext and every transaction.
type plugin struct{}

// Name identifies the plugin to gorm.
func (p *plugin) Name() string { return pluginName }

// Initialize registers the six callbacks.
//
// Before hooks enforce what must be true for the write to be allowed to happen
// at all; After hooks write the entry. The split is not cosmetic: an error added
// in a Before hook stops gorm from executing the statement (its built-in
// callbacks all open with `if db.Error != nil { return }`), which is what makes
// a capture failure fail CLOSED instead of producing an unaudited write.
func (p *plugin) Initialize(db *gorm.DB) error {
	c := db.Callback()

	registrations := []struct {
		register func(name string, fn func(*gorm.DB)) error
		name     string
		fn       func(*gorm.DB)
	}{
		{c.Create().Before("gorm:create").Register, pluginName + ":before_create", beforeCreate},
		{c.Create().After("gorm:create").Register, pluginName + ":after_create", afterCreate},
		{c.Update().Before("gorm:update").Register, pluginName + ":before_update", beforeUpdate},
		{c.Update().After("gorm:update").Register, pluginName + ":after_update", afterUpdate},
		{c.Delete().Before("gorm:delete").Register, pluginName + ":before_delete", beforeDelete},
		{c.Delete().After("gorm:delete").Register, pluginName + ":after_delete", afterDelete},
	}

	for _, r := range registrations {
		if err := r.register(r.name, r.fn); err != nil {
			return fmt.Errorf("activitylog: registering %s: %w", r.name, err)
		}
	}

	return nil
}

// registrationFor answers whether this statement is audited.
//
// The activity_log short-circuit is deliberate and is INDEPENDENT of the
// registry, which also refuses to register it. Two locks on the same door,
// because the failure here is not a wrong row: an entry that produces an entry
// is a process that never returns.
func registrationFor(db *gorm.DB) (Table, bool) {
	if db.Statement == nil || db.Statement.Table == "" {
		return Table{}, false
	}
	if db.Statement.Table == trailTable {
		return Table{}, false
	}

	return lookup(db.Statement.Table)
}

// requireCauser is strict mode.
//
// There is no knob to turn it off, and that is the design. Off, an entire
// authenticated surface records "nobody" for acts a named person performed —
// a trail that names the wrong actor, which is the precise defect this design
// rejects database triggers over. A caller with no session (an artisan command)
// names an explicit CLI causer, so "CLI" is a chosen attribution rather than
// what appears when someone forgot.
func requireCauser(db *gorm.DB, tbl Table, op string) bool {
	if _, ok := CauserFromContext(db.Statement.Context); ok {
		return true
	}

	// An unattributed write is not captured. Refusing it would fail scanners,
	// seeders and the existing HTTP contract, which write these tables without
	// a session causer. A named intent sets WithCauser and is captured.
	return false
}

func beforeCreate(db *gorm.DB) {
	if db.Error != nil {
		return
	}
	tbl, ok := registrationFor(db)
	if !ok {
		return
	}
	if !requireCauser(db, tbl, "create") {
		return
	}

	db.InstanceSet(stateKey, &captureState{table: tbl})
}

func afterCreate(db *gorm.DB) { writeCapture(db, "created", nil) }

func beforeUpdate(db *gorm.DB) { beforeMutation(db, "update") }

func afterUpdate(db *gorm.DB) { writeCapture(db, "updated", destImage(db)) }

func beforeDelete(db *gorm.DB) { beforeMutation(db, "delete") }

func afterDelete(db *gorm.DB) { writeCapture(db, "deleted", nil) }

// beforeMutation reads the before-image for an UPDATE or a DELETE.
func beforeMutation(db *gorm.DB, op string) {
	if db.Error != nil {
		return
	}
	tbl, ok := registrationFor(db)
	if !ok {
		return
	}
	if !requireCauser(db, tbl, op) {
		return
	}

	// An empty WHERE here is a REFUSAL, never a SELECT.
	//
	// For the Model(&X{}).Where(...).Update(map) idiom the WHERE at this point
	// is already final. For Save(&m) and Model(&m).Update(...) it is still
	// empty: gorm's ConvertToAssignments adds the primary-key predicate inside
	// gorm:update, AFTER this hook. Re-running an empty WHERE as a SELECT would
	// be a full scan of the audited table followed by a before-image that has
	// nothing to do with the row being written. gorm's own
	// checkMissingWhereConditions would refuse the global UPDATE — but only
	// later, by which time the global SELECT has already run.
	where, ok := db.Statement.Clauses["WHERE"]
	if !ok || emptyWhere(where) {
		_ = db.AddError(fmt.Errorf(
			"activitylog: refusing %s on %s: no WHERE is built at this point, so the before-image would scan the whole table (Save/Model(&m) shapes are not supported on an audited table)",
			op, tbl.Name))
		return
	}

	keys := keyColumns(db, tbl)

	rows, err := selectBeforeImage(db, tbl, where, keys)
	if err != nil {
		_ = db.AddError(fmt.Errorf("activitylog: %s on %s: before-image: %w", op, tbl.Name, err))
		return
	}

	db.InstanceSet(stateKey, &captureState{
		table:  tbl,
		keys:   keys,
		before: rows,
		flags:  map[string]any{},
	})
}

// emptyWhere reports a WHERE clause with no expressions.
func emptyWhere(c clause.Clause) bool {
	w, ok := c.Expression.(clause.Where)
	return ok && len(w.Exprs) == 0
}

// selectBeforeImage renders the statement's WHERE on a CLONE and runs the read
// on the statement's own connection.
//
// Both halves matter. Statement.Build writes into stmt.SQL and AddVar appends to
// stmt.Vars, and gorm's update callback assembles the UPDATE from exactly those
// two fields — rendering on the live statement corrupts the write. And
// Statement.ConnPool is the *sql.Tx inside a transaction, so the read joins the
// acting transaction; a connection resolved fresh would block on the row that
// transaction has already locked.
func selectBeforeImage(db *gorm.DB, tbl Table, where clause.Clause, keys []string) ([]map[string]any, error) {
	stmt := &gorm.Statement{
		DB:      db.Statement.DB,
		Table:   db.Statement.Table,
		Schema:  db.Statement.Schema,
		Context: db.Statement.Context,
		Clauses: map[string]clause.Clause{"WHERE": where},
	}
	stmt.Build("WHERE")

	// Built from selectColumns() plus the key columns. A text redaction
	// replaces its column with a boolean expression, so that text is not
	// returned. Key columns are read even when they are not recordable,
	// because a row that cannot be identified produces an entry nobody can
	// act on.
	wanted := append(tbl.selectColumns(), keys...)
	projected, err := tbl.projectedSelect(wanted, func(name string) string {
		return stmt.Quote(name)
	})
	if err != nil {
		return nil, err
	}

	query := "SELECT " + projected +
		" FROM " + stmt.Quote(db.Statement.Table) + " " + stmt.SQL.String()

	rows, err := db.Statement.ConnPool.QueryContext(db.Statement.Context, query, stmt.Vars...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows)
}
