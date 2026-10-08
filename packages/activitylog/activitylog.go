// Package activitylog is a portable audit trail for Goravel applications: it
// captures created/updated/deleted with before/after images through a gorm
// plugin, and refuses to record a write it cannot attribute.
//
// # What it is a port of, and what it is not
//
// It is a port of spatie/laravel-activitylog. It is NOT a port of that
// package's mechanism. The Eloquent trait has no working equivalent here:
// Goravel's orm.Observer never fires on the update idiom this kind of codebase
// uses (framework/database/gorm/query.go gates update and delete events on the
// model struct carrying its primary key), and its Event.GetOriginal reflects
// over the struct the caller passed rather than reading the row. Capture is
// therefore a gorm plugin, one level below the ORM. See
// docs/specs/2026-08-27-activity-log-design.md §3.
//
// # Why it imports nothing from the host application
//
// tests/architecture enforces an EMPTY allow-set for this directory: no
// app/models, no app/facades, nothing under the module path. That is the whole
// portability guarantee, and it is mechanical rather than a convention — the
// day a convenience import lands, CI fails in the commit that broke it instead
// of on the day someone tries to install the package elsewhere.
//
// Everything application-specific therefore arrives as DATA through Register
// (registry.go): which tables are audited, which columns may be recorded, and
// how a jsonb document is redacted.
package activitylog

import "github.com/goravel/framework/contracts/foundation"

// Binding is the container key the facade resolves.
const Binding = "activitylog"

// App is set by ServiceProvider.Register so facades/ can reach the container.
var App foundation.Application

// ActivityLog implements contracts.ActivityLog.
type ActivityLog struct{}
