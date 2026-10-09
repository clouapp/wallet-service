> Status: HOLDS as convention (no test guards the shape; `tests/architecture`
> guards only the direction, `http → repositories` and `services → repositories`).
> A miss is a sentinel, every method takes a `context.Context`, and queries live
> here.

Every query lives in `app/repositories`. A repository is a concrete struct that
embeds `db.Base` and has **no interface of its own** — the consumer declares the
port. Every constructor takes the `orm.Query` first (`NewX(nil)` resolves a fresh
query per call; `NewX(tx)` binds one transaction); a repository that needs a
collaborator takes it as the next argument (`NewWebhookConfigRepository(query,
cipher)`). Context is an argument of every method; the singleton never stores
a request context. A transaction can also travel on the context (`WalletRepository.Within(ctx, fn)`,
`db.WithTx`): `fn` gets a context whose queries join it.

`app/repositories/internal/db` is visible only to `app/repositories`.

## `internal/db`

The query seam (`Base.Query(ctx)`), joining a transaction already open
(`Base.Transaction(ctx, fn)`, which joins one instead of starting a second), and
the translators: `RequireRow` (no row affected → `models.ErrRepositoryNotFound`)
and `LookupError`, which turns a `FirstOrFail` miss (`OrmRecordNotFound`) into the
bare `models.ErrRepositoryNotFound` and wraps anything else with the operation.
Do not use `First` and a zero-ID check. A duplicate key is recognised with
`pkg/pgerr.IsUniqueViolation` (SQLSTATE 23505). No business rule lives here.

## How a method is shaped

- **A miss is a sentinel, never `(nil, nil)` and never a zero struct.** A
  caller that has to remember to nil-check a successful return is a caller that
  eventually does not.
- **One method per intent, naming its columns** — `SetStatus`, `MarkBroadcasted`,
  `RecordFailure` — rather than `UpdateField(id, field, value)`. A generic updater
  lets any caller write any column, including ones a request snapshot read before
  a concurrent change. (`WebhookConfigRepository.UpdateFields` is the one
  remaining generic updater, kept because it seals the secret on the way in.)
- A repository writes its own table. Work that must change another table calls
  that table's repository with the same `tx`.
- A read that must be serialised takes the row lock (`LockForUpdate()`) inside the
  service's transaction: balance, UTXO and withdrawal-state changes that race
  read it that way.
- `UPDATE ... RETURNING` that matches nothing is an empty scan: check `len` and
  return `ErrRepositoryNotFound`.
- Pagination returns `(rows, total, err)` and takes `limit, offset int` (the window comes from `pagination.ParseParams`).
- Amounts are stored and returned as base-unit strings/`numeric`; conversion is
  `pkg/amount`'s job, never a float.
- Wrap with the query name: `fmt.Errorf("insert withdrawal: %w", err)`.

A function comment is one English line and says what the function returns and
when it fails.

Seeders and migrations are the only other places SQL may appear, and only for
schema and seed data.
