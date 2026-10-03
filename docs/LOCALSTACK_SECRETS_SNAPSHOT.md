# LocalStack Secrets Manager snapshots

LocalStack community keeps nothing across restarts. Secrets Manager holds MPC share B, so the
`waas-localstack` container snapshots every secret into the `localstack_data` volume
(`/var/lib/localstack/secrets-snapshot`) and restores them on start.

## Format (unchanged since the former Python tool)

- `secrets-<%Y%m%dT%H%M%S%fZ>.json.gpg`: gpg symmetric, AES256, s2k-mode 3, SHA512,
  s2k-count 65011712, passphrase file = `~/.config/macro-wallets/localstack-seed/seed.key`
  (mounted read-only at `/run/secrets/localstack-seed/seed.key`; never in the repo, never printed).
- `secrets-<stamp>.meta.json`: `{count, live_count, digest, created_at}`; `digest` is the sha256 of
  the compact, ensure_ascii JSON of the sorted `[name, arn, binary_b64, string]` tuples.
- Restore keeps the original ARN suffix (moto `_custom_id_` tag) and the version id.
- Export refuses until this boot restored (`/tmp/secrets-snapshot.restored` = boot id), keeps the
  20 newest snapshots, and skips unchanged material (silently in the loop).

## Go tool

`tools/localstack-secrets-snapshot` (package `snapshot`, tests with Python-written fixtures):

```bash
make localstack-hooks   # static linux binary -> docker/localstack/bin/secrets-snapshot (gitignored)
secrets-snapshot export [--during-shutdown] | restore | verify [--all] | loop
```

`verify` is a read-only dry run of `restore` (decrypt, check meta, compare with live).
`make docker-up` and `make dev` rebuild the binary first. Env vars: `SECRETS_SNAPSHOT_DIR`,
`_KEY_FILE`, `_INTERVAL`, `_KEEP`, `_FORCE`, `_ACCOUNT_ID`, `_ENDPOINT_URL` (default `http://localhost:4566`).

## Wiring in waas-localstack

```yaml
- ./docker/localstack/bin:/etc/localstack/secrets-snapshot:ro
- ./docker/localstack/init/ready.d/50-secrets-snapshot.sh:/etc/localstack/init/ready.d/50-secrets-snapshot.sh:ro
- ./docker/localstack/init/shutdown.d/50-secrets-snapshot.sh:/etc/localstack/init/shutdown.d/50-secrets-snapshot.sh:ro
```

- `ready.d`: `restore`, then `loop` detached (log: `/var/lib/localstack/logs/secrets-snapshot.log`).
  A failed restore leaves the loop off so an empty LocalStack never overwrites a snapshot.
- `shutdown.d`: `export --during-shutdown` marks its requests as LocalStack-internal
  (`x-localstack-data` header), which the gateway still serves while external calls get 503.
  Its line (`export: unchanged` or `exported N secret(s)`) lands in `docker logs waas-localstack`.
- The binary is bind-mounted as a directory: `make localstack-hooks` replaces it in place, and the
  new build runs from the next `docker restart`/`stop`+`start` (no recreation needed).

The container was created with `--env-file .env.dev` (ports 4567/5433/6380 come from it); any
`docker compose` call on it must pass the same file.

## Checks

```bash
docker exec waas-localstack /etc/localstack/secrets-snapshot/secrets-snapshot verify
wallet-vault.py localstack-export && wallet-vault.py localstack-check   # macro-markets repo
```

`localstack-check` must report `missing_live=0 not_in_export=0 different_value=0 different_arn=0`.
To exercise the shutdown hook, use `docker stop waas-localstack` + `docker start waas-localstack`
(never recreate) and look for its line in `docker logs`.

Observed: LocalStack 4.12.1 stays in `[shutdown] Stopping all services` until `stop_grace_period`
(30 s) and exits 137 (the last Python-hook container also took ~30 s to stop). The shutdown export
runs before that point, so it is not affected.
