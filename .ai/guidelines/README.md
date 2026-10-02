# Guidelines

The rules an agent or reviewer must not break, one file per concern. `CLAUDE.md`
holds the invariants of the system as a whole; these files hold the shape of the
code that implements them.

> Status: these files describe the TARGET shape. Most of the code does not follow
> it yet — the migration plan is `macro-wallets-alignment-prompt.md`. Each file
> says what already holds. New code follows the target from day one; touched code
> moves toward it in the same change.

| File | Covers |
|---|---|
| [`http-layer.md`](./http-layer.md) | `requests` / `responses` / `resources` / `middleware`, the shape of a handler, validation, the global chain |
| [`controllers-and-services.md`](./controllers-and-services.md) | the layers, constructor injection, DTOs, what a controller may not do |
| [`service-layout.md`](./service-layout.md) | one subject one service, file names, narrow store interfaces |
| [`repository-layer.md`](./repository-layer.md) | where a query lives, `internal/db`, how a repository method is shaped |
| [`identity-and-scope.md`](./identity-and-scope.md) | who is asking (a user or an API token) and the account → wallet scope chain |
| [`authorization.md`](./authorization.md) | the three layers a decision can live in, roles, the account environment, the inventory |
| [`http-error-contract.md`](./http-error-contract.md) | the error envelope, the code list, what each status means, what a body may carry |
| [`errors-and-logging.md`](./errors-and-logging.md) | sentinels per package, wrapping, the redacted log sink, what is never logged |
| [`custody-and-signing.md`](./custody-and-signing.md) | MPC shares, the passphrase, Secrets Manager, what may touch key material |
| [`chain-adapters.md`](./chain-adapters.md) | one adapter per chain/provider, the registry, RPC URLs, provider failures |
| [`queues-and-workers.md`](./queues-and-workers.md) | the Goravel queue, SQS, localworkers, jobs and events |
| [`mail-and-notifications.md`](./mail-and-notifications.md) | mailables, why a credential mail is `Send` inside a job and never `Queue` |
| [`testing.md`](./testing.md) | where a test lives, the HTTP suite, mockery, concurrency guards, fixtures, testnet e2e |

These files are the ONE home for the shape of the code. `.cursor/rules/waas.mdc`
is a pointer to here, not a second copy: a second copy of a rule is a rule that rots.
If a rule here and a test in `tests/architecture` disagree, the test is the rule —
fix the prose.

## Deliberate choices

Where a rule goes against a common default on purpose, the file says so inline —
do not "fix" it back:

- controllers are `app/http/controllers/<surface>/<feature>/` with exactly two
  surfaces, `dashboard` (session, `/v1`) and `external` (API token, `/api/v1`).
  A handler is never shared between surfaces and never branches on which caller
  it has (`http-layer.md`);
- JSON is written with `encoding/json` through `responses.JSON`, never
  `ctx.Response().Json()` (`http-layer.md`);
- models carry no wire tags — not even `json:"-"`; the wire shape lives only in
  `app/http/resources` (`http-layer.md`);
- the error envelope is **[DECISION 2.1 — fill in]** with the codes in
  `app/http/resources/error_resource.go` (`http-error-contract.md`);
- `app/policies` is the one arbiter of "who may do what"; route middleware, Gate
  abilities and services all ask it (`authorization.md`);
- key material never leaves `app/services/mpc` and the custody services, and is
  never logged, returned or put in a payload (`custody-and-signing.md`);
- a credential e-mail is `Send` inside a job that carries no credential, and
  `facades.Mail().Queue()` refuses (`mail-and-notifications.md`);
- repositories have no interface of their own and are never mocked; services
  are tested against the ports they declare (`testing.md`).
