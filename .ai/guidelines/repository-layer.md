# Repository layer

> Status: TARGET. Today repositories are interfaces over empty structs calling
> `facades.Orm()` inline, with no `context.Context`, `(nil, nil)` on a miss and
> generic `UpdateField(s)`. Queries also live in `services/chainregistry`,
> `rules/`, `console/commands` and `controllers/testutil`. Migration: §3.7.

Every query lives in `app/repositories`. A repository is a concrete struct that
embeds `db.Base` and has **no interface of its own** — the consumer declares the
port. `NewX(nil)` resolves a fresh query per call; `NewX(tx)` binds one
transaction. Context is an argument of every method; the singleton never stores
a request context.

`app/repositories/internal` is visible only to `app/repositories`.

## `internal/db`

The query seam (`Base.Query(ctx)`), joining a transaction already open
(`Transaction(ctx, fn)`, which rolls back and re-panics when `fn` panics), and
`RequireRow` (no row affected → `models.ErrRepositoryNotFound`). `First` does
not fail on a miss: the repository checks the zero ID. A duplicate key is
recognised with `pkg/pgerr.IsUniqueViolation` (SQLSTATE 23505). No business
rule lives here.

## `internal/types`

Scan targets that are not models and fixed lookup maps. Not a place for domain
logic.

## How a method is shaped

- **A miss is a sentinel, never `(nil, nil)` and never a zero struct.** A
  caller that has to remember to nil-check a successful return is a caller that
  eventually does not.
- **One method per intent, naming its columns** — `SetStatus`, `MarkBroadcasted`,
  `RecordFailure` — never `UpdateField(id, field, value)` or
  `UpdateFields(id, map[string]any)`. A generic updater lets any caller write any
  column, including ones a request snapshot read before a concurrent change.
- A repository writes its own table. Work that must change another table calls
  that table's repository with the same `tx`.
- A read and the same read with `FOR UPDATE` are one method with a `lock bool`.
  Balance, UTXO and withdrawal-state changes that race read with `lock: true`
  inside the service's transaction.
- `UPDATE ... RETURNING` that matches nothing is an empty scan: check `len` and
  return `ErrRepositoryNotFound`.
- Pagination returns `(rows, total, err)` and takes a `dtos.Page` (limit, offset).
- Amounts are stored and returned as base-unit strings/`numeric`; conversion is
  `pkg/amount`'s job, never a float.
- Wrap with the query name: `fmt.Errorf("insert withdrawal: %w", err)`.

A function comment is one English line and says what the function returns and
when it fails.

Seeders and migrations are the only other places SQL may appear, and only for
schema and seed data.
