package activitylog

import (
	"context"
	"sync"
)

const (
	// CauserUsers is a dashboard user.
	CauserUsers = "users"
	// CauserPlatformAdmins is a platform operator.
	CauserPlatformAdmins = "platform_admins"
	// CauserAPITokens is an account API token. The label is the token name.
	CauserAPITokens = "api_tokens"
	// CauserCLI is an artisan command.
	CauserCLI = "cli"
)

// Causer is who performed the act, snapshotted.
//
// Label is an address AT THE TIME — never resolved later through a foreign key,
// because a record a user deletion can erase or null is not an audit record.
// This is the rule snapshotted actor labels already follow in this install.
type Causer struct {
	Type  string
	ID    string
	Label string
}

// Intent is the half of the story a statement cannot carry: that a users UPDATE
// is really a sign-in, or that a DELETE and an INSERT over
// publisher_role_permissions are "the owner rewrote what admin grants".
//
// A gorm callback sees a table, a set of columns and a WHERE. It cannot see
// purpose. This is the equivalent of the argument spatie's activity()->log()
// takes. See docs/specs/2026-08-27-activity-log-design.md §5.10.
//
// An intent applies to one statement. A multi-statement act labels each write
// with WithIntent again (repositories preserve per-call context inside a
// transaction via orm.QueryWithContext).
type Intent struct {
	Event       string
	Description string
}

type (
	causerKey struct{}
	scopeKey  struct{}
	batchKey  struct{}
	intentKey struct{}
)

// intentCell makes "consumed once" possible over an immutable context: the
// context holds the pointer and the read swaps the contents out.
//
// Guarded because one request's writes can be concurrent, and this is the only
// mutable state the package puts in a context.
type intentCell struct {
	mu     sync.Mutex
	intent Intent
	used   bool
}

// WithCauser attaches the actor. Callers take it from the SESSION, never from a
// request body. The value is fixed for the life of the context — name it once
// the flow knows who is acting, then pass that context to later writes.
func WithCauser(ctx context.Context, c Causer) context.Context {
	return context.WithValue(ctx, causerKey{}, c)
}

// CauserFromContext returns the actor and whether one was attached at all.
//
// The boolean is the point. ABSENT and empty need opposite readings: under
// strict mode an absent causer refuses the write, while a causer filled with ""
// would be an attribution to nobody — a trail that names the wrong actor, which
// is worse than a trail with a hole and is exactly why this design rejects
// database triggers (spec §3.2).
func CauserFromContext(ctx context.Context) (Causer, bool) {
	c, ok := ctx.Value(causerKey{}).(Causer)
	return c, ok
}

// WithScope attaches the host application's publisher scoping dimension.
func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// ScopeFromContext returns the scope, or "" for the platform.
func ScopeFromContext(ctx context.Context) string {
	s, _ := ctx.Value(scopeKey{}).(string)
	return s
}

// WithBatch attaches the id that groups one request's statements.
func WithBatch(ctx context.Context, batch string) context.Context {
	return context.WithValue(ctx, batchKey{}, batch)
}

// BatchFromContext returns the batch id, or "".
func BatchFromContext(ctx context.Context) string {
	b, _ := ctx.Value(batchKey{}).(string)
	return b
}

// WithIntent names what the next captured statement is FOR.
//
// It applies to one statement. A caller that means to label several should set
// it again on each write.
func WithIntent(ctx context.Context, i Intent) context.Context {
	return context.WithValue(ctx, intentKey{}, &intentCell{intent: i})
}

// takeIntent returns the intent for the next captured statement and clears it,
// so it cannot leak onto an unrelated write later in the same request.
func takeIntent(ctx context.Context) (Intent, bool) {
	cell, ok := ctx.Value(intentKey{}).(*intentCell)
	if !ok {
		return Intent{}, false
	}

	cell.mu.Lock()
	defer cell.mu.Unlock()

	if cell.used {
		return Intent{}, false
	}
	cell.used = true

	return cell.intent, true
}

// SystemCauser names a non-interactive actor: an artisan command, a scheduled
// job, a seeder.
//
// It exists so that "the CLI did this" is a CHOSEN attribution rather than what
// appears when somebody forgot. Strict mode refuses an unattributed write, so a
// command that omits this fails loudly at its own call site instead of quietly
// writing an anonymous row in production.
func SystemCauser(name string) Causer {
	return Causer{Type: "system", ID: name, Label: name}
}
