# Test Database Isolation

## Goal

Prevent every Go test command from migrating, truncating, or dropping the development database.

## Design

- Commit a safe `.env.testing` that targets PostgreSQL database `vault_test` in the existing development PostgreSQL container and Redis logical database 15.
- Load `.env.testing` with override semantics before Goravel boots, so variables inherited from `.env.dev` cannot win.
- Parse and apply the testing database configuration once in `tests/testutil`; remove the ineffective `TEST_DATABASE_URL` reads and hardcoded duplicate configuration.
- Before every `migrate:fresh`, fail fast unless `APP_ENV=testing` and the configured database name ends with `_test`.
- Make test targets create `vault_test` idempotently before invoking `go test`.

## Verification

- Unit-test environment loading, DSN parsing, and destructive-operation guards.
- Insert a sentinel row in `vault`, run a database-backed test against `vault_test`, and prove the sentinel remains.
- Run the affected backend test packages with `TEST_DB_REQUIRED=1`.

## Non-goals

- A second PostgreSQL or Redis container.
- Per-test databases or schemas.
- Changes to production or development connection settings.
