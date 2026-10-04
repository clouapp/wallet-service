package activitylog

import (
	"context"
	"errors"
	"fmt"
)

// RecordInput is one manually recorded act.
//
// Table ties it to a registration: the log name, the subject name and the
// column allowlist all come from there.
type RecordInput struct {
	Table       string
	Event       string
	Description string
	SubjectID   string
	Old         map[string]any
	New         map[string]any
}

// Record appends one entry for an act automatic capture cannot see.
//
// It applies the SAME redaction as the plugin: the table's registration decides
// which columns of Old and New survive, and a jsonb rule prunes inside them. A
// manual entry that could publish a column the automatic path filters would make
// the allowlist advisory.
//
// The actor comes from the context, on the same terms as automatic capture — a
// caller cannot name one. Strict auditing applies too: an unattributed manual
// record is refused rather than written as nobody's.
//
// # Where it writes, and what that costs
//
// Outside a callback there is no statement to borrow a connection from, so this
// writes through the ORM's *sql.DB. The consequence to know: an entry recorded
// while the caller is inside a transaction lands OUTSIDE it and survives a
// rollback. Automatic capture does not have that problem — it writes on
// Statement.ConnPool, which is the transaction. Prefer letting the plugin do the
// recording wherever the write goes through the ORM at all.
func (r *ActivityLog) Record(ctx context.Context, in RecordInput) error {
	if in.Table == "" {
		return errors.New("activitylog: record: table is required")
	}

	tbl, ok := lookup(in.Table)
	if !ok {
		return fmt.Errorf("activitylog: record: %s is not registered", in.Table)
	}

	causer, ok := CauserFromContext(ctx)
	if !ok {
		return fmt.Errorf("activitylog: refusing to record %s on %s: no causer in context", in.Event, in.Table)
	}

	entry := Entry{
		LogName:     tbl.LogName,
		Scope:       ScopeFromContext(ctx),
		Event:       in.Event,
		Description: in.Description,
		SubjectType: tbl.Subject,
		SubjectID:   in.SubjectID,
		CauserType:  causer.Type,
		CauserID:    causer.ID,
		CauserLabel: causer.Label,
		BatchUUID:   BatchFromContext(ctx),
		Properties:  map[string]any{},
	}

	if intent, ok := takeIntent(ctx); ok {
		if intent.Event != "" {
			entry.Event = intent.Event
		}
		if intent.Description != "" {
			entry.Description = intent.Description
		}
	}

	flags := map[string]any{}
	if len(in.Old) > 0 {
		img, raised := tbl.image(in.Old)
		entry.Properties["old"] = img
		for k, v := range raised {
			flags[k] = v
		}
	}
	if len(in.New) > 0 {
		img, raised := tbl.image(in.New)
		entry.Properties["new"] = img
		for k, v := range raised {
			flags[k] = v
		}
	}
	for k, v := range flags {
		entry.Properties[k] = v
	}

	pool, err := recordPool()
	if err != nil {
		return err
	}

	return writeEntry(ctx, pool, entry)
}

// recordPool resolves the connection a manual record writes through.
func recordPool() (ExecPool, error) {
	if App == nil {
		return nil, errors.New("activitylog: record: the package is not booted")
	}

	orm := App.MakeOrm()
	if orm == nil {
		return nil, errors.New("activitylog: record: no orm configured")
	}

	db, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("activitylog: record: resolving the connection: %w", err)
	}

	return db, nil
}
