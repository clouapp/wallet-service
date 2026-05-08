# Dev seed data

Reference for the fixtures created by `make db-seed` / `make migrate-fresh-seed`.
All values are test-only — LocalStack, devnet/testnet RPCs, deterministic UUIDs.
Safe to commit. **Never use any of these values against mainnet.**

Seed logic: `database/seeds/` · invoked by `database/seeders/DatabaseSeeder`.

---

## Login credentials

| Email | Password | Role |
|---|---|---|
| `admin@macro.markets` | `secret` | Account owner |
| `alice@macro.markets` | `secret` | Account admin |
| `bob@macro.markets` | `secret`  | Auditor |

User UUIDs (`database/seeds/ids.go`):

| User | ID |
|---|---|
| admin | `00000000-0000-0000-0000-000000000001` |
| alice | `00000000-0000-0000-0000-000000000002` |
| bob   | `00000000-0000-0000-0000-000000000003` |

---

## Accounts

Paired (linked by `linked_account_id`, default selection = prod):

| Environment | Name | ID |
|---|---|---|
| prod | Acme Corp | `00000000-0000-0000-0000-000000000010` |
| test | Acme Corp (Test) | `00000000-0000-0000-0000-000000000011` |

Dashboard requests must send `X-Account-Id: <id>` to scope queries.

---

## MPC wallets

Every seeded wallet is **real**: `mpc.Keygen` runs at seed time (secp256k1 or
ed25519), `ShareA` is encrypted with `SeedPassphrase`, `ShareB` is stored in
LocalStack Secrets Manager at `vault/wallet/<wallet_id>/share-b`, and the
deposit address is derived from the combined public key.

**Seed passphrase (every wallet):** `macro-seed-pass-2026`

Addresses are regenerated on every `make migrate-fresh-seed` (new MPC ceremony
each time), so they will differ from the table below on your machine. Query the
live values with:

```sql
SELECT w.id, w.chain, w.label, a.address
FROM wallets w
JOIN addresses a ON a.id = w.deposit_address_id
ORDER BY w.id;
```

### Prod account (`...010`)

| Chain | Wallet ID | Label | Curve |
|---|---|---|---|
| `eth`     | `00000000-0000-0000-0000-000000000020` | Primary ETH Wallet  | secp256k1 |
| `btc`     | `00000000-0000-0000-0000-000000000021` | Primary BTC Wallet  | secp256k1 |
| `polygon` | `00000000-0000-0000-0000-000000000022` | Polygon Wallet      | secp256k1 |
| `sol`     | `00000000-0000-0000-0000-000000000026` | Primary SOL Wallet  | ed25519   |

### Test account (`...011`)

| Chain | Wallet ID | Label | Curve |
|---|---|---|---|
| `teth`     | `00000000-0000-0000-0000-000000000023` | Sepolia ETH Wallet     | secp256k1 |
| `tbtc`     | `00000000-0000-0000-0000-000000000024` | Bitcoin Testnet Wallet | secp256k1 |
| `tpolygon` | `00000000-0000-0000-0000-000000000025` | Polygon Amoy Wallet    | secp256k1 |
| `tsol`     | `00000000-0000-0000-0000-000000000027` | Solana Devnet Wallet   | ed25519   |

### Sample addresses (last `migrate-fresh-seed` run)

Kept here for reference. Not a guarantee — re-seeding mints new keys.

```
eth       0x73c2c499738c93d716111f2fdc7bbdc05f34ba76
btc       bc1qwgd98pvzv5z24du8tt0j9qf7cy3ml9cjr50k88
polygon   0xdde7f8c47a6e34d9762db256b8486495c91aeaac
sol       oSv2G55v7Lzpz8w2GkZJg3xTxfj9WTQQarm28PM2AN6
teth      0x97c9b95bebc35cf2b774e0185a2908be7f3081e4
tbtc      tb1q9rh4d3ycvlzauszyuu2gn04l2xqmw0gf4eux67
tpolygon  0xa10e70d085ba4f3776279ce3a39e8bdd02f9314d
tsol      8rogSDj9Pzprr4PKqXDK5LruGPzrKPpCJVGE9BEaeTsx
```

---

## Wallet memberships (`wallet_users`)

| Wallet | alice | bob |
|---|---|---|
| eth / btc / teth / tbtc | `viewer,spender` | `viewer` |
| polygon / tpolygon      | `viewer`         | —       |
| sol / tsol              | `viewer,spender` | —       |

---

## Using the fixtures

Login and hit any dashboard endpoint:

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:2002/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"alice@macro.markets","password":"secret"}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["access_token"])')

curl -s http://127.0.0.1:2002/v1/wallets \
  -H "Authorization: Bearer $TOKEN" \
  -H 'X-Account-Id: 00000000-0000-0000-0000-000000000011'
```

Sign/withdraw operations use the seed passphrase `macro-seed-pass-2026` to
decrypt `ShareA`.

---

## Resetting

| Command | Effect |
|---|---|
| `make db-seed`            | Re-runs seeds; refreshes `chains.rpc_url` under the current `APP_KEY` (self-heal after key rotation); leaves existing wallets untouched. |
| `make migrate-fresh-seed` | Drops every table, re-migrates, re-seeds. New MPC ceremony → new addresses. |

`ShareB` secrets in LocalStack survive a `migrate-fresh` — the seed reuses the
existing ARN via `PutSecretValue` when `CreateSecret` hits `ResourceExists`.
