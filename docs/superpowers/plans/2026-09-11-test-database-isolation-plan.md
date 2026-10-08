# Test Database Isolation Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task.

**Goal:** Guarantee that Go tests can only run destructive migrations against `vault_test`, never `vault`.

**Architecture:** A small `tests/testenv` package loads `.env.dev` for local infrastructure coordinates, then overrides safety-critical values from committed `.env.testing`. Every test bootstrap uses it, and `mocks.TestDB` revalidates the active Goravel connection immediately before each `migrate:fresh`. Make targets provision `vault_test` and serialize DB-backed packages.

**Tech Stack:** Go 1.22, Goravel, PostgreSQL 15, Redis 7, `godotenv`, Make.

---

### Task 1: Testing environment loader and guard

**Files:**
- Create: `.env.testing`
- Create: `tests/testenv/environment.go`
- Create: `tests/testenv/environment_test.go`

- [ ] Write tests proving `Validate` accepts only `APP_ENV=testing` with a database ending in `_test`, and rejects `vault`, empty names, and non-testing environments.
- [ ] Run `go test ./tests/testenv -count=1`; expect failure because the package does not exist.
- [ ] Implement repository-root discovery, `.env.dev` loading without override, `.env.testing` loading with override, and fail-fast validation.
- [ ] Run `go test ./tests/testenv -count=1`; expect PASS.

### Task 2: Route all database tests through the safe environment

**Files:**
- Modify: `tests/testutil/boot.go`
- Modify: `tests/mocks/testdb.go`
- Modify: `app/http/controllers/main_test.go`
- Modify: `app/http/middleware/api_wallet_context_test.go`

- [ ] Add a test for configuration extraction with host, port, database, username, password, and SSL mode sourced from the loaded testing environment.
- [ ] Run the focused test; expect failure while `BootTest` still hardcodes port 5432 and ignores the DSN/environment.
- [ ] Load and validate `.env.testing` before every lightweight or full Goravel boot.
- [ ] Remove the unused `TEST_DATABASE_URL` paths and duplicate hardcoded test DB override.
- [ ] Validate the active Goravel database name and `APP_ENV` immediately before setup and cleanup `migrate:fresh` calls.
- [ ] Run focused `tests/testutil`, repository, controller, and middleware tests with `TEST_DB_REQUIRED=1`; expect PASS against `vault_test`.

### Task 3: Provision and serialize the test database

**Files:**
- Create: `docker/postgres/init-test-database.sh`
- Modify: `docker-compose.yml`
- Modify: `Makefile`
- Modify: `.env.dev.example`

- [ ] Add an idempotent PostgreSQL initialization script that creates `${POSTGRES_DB}_test`.
- [ ] Mount the script under `/docker-entrypoint-initdb.d`.
- [ ] Add a Make helper that creates `vault_test` for existing volumes.
- [ ] Make all local Go test targets load `.env.testing`, export `TEST_DB_REQUIRED=1`, and use `go test -p 1`.
- [ ] Remove `TEST_DATABASE_URL` from development environment documentation because Goravel tests now use `DB_*` from `.env.testing`.

### Task 4: Prove development data survives

**Files:**
- Verify: `.env.testing`
- Verify: `tests/testenv/environment_test.go`
- Verify: `Makefile`

- [ ] Record `vault` user and chain counts before testing.
- [ ] Run a representative DB-backed package through `make` against `vault_test`.
- [ ] Re-read `vault` counts and prove they are unchanged.
- [ ] Query `vault_test.migrations` to prove migrations ran in the test database.
- [ ] Run type-independent backend tests and lint checks for changed files.
