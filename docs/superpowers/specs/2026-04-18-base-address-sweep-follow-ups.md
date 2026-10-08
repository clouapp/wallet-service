# Base Address + Sweep — Follow-up Items (non-blocking)

Collected from code review comments during the implementation of the
`feature/base-address-sweep` branch (spec: `2026-04-18-base-address-sweep-design.md`,
plan: `2026-04-18-base-address-sweep.md`).

## Hygiene / minor

1. **`timestamp` vs `timestamptz` inconsistency** — some timestamp columns use
   Postgres `timestamp` via Goravel `table.Timestamp(...)` while model GORM
   tags declare `timestamptz`. Normalize project-wide (e.g. add a `table.TimestampTz`
   migration for the 2 affected columns: `wallets.gas_last_checked_at`, 
   `wallets.frozen_until`).
2. **Env key naming `POL_*` vs chain ID `polygon`** in `config/security.go` 
   `SweepDefaults` — rename to `POLYGON_*` / `TPOLYGON_*` for consistency before
   operator docs ship.
3. **`gas_status` without CHECK constraint** — migration uses `varchar(16)` with a
   doc comment listing allowed values; precedent in the project uses Postgres enums
   (`wallet_status`, `wallet_read_model_status`). Promote to an enum before heavy
   external usage.
4. **Partial enum mirror** — `models.TxType*` constants cover only 4 of the 7
   values in the `transaction_type` Postgres enum; other values (`transfer`,
   `token_transfer`, `fee`) are still string literals across services. Mirror in
   full.
5. **`chains.threshold*` exposure via JSON** — threshold columns are tagged
   `json:"..."`, which leaks operational config to API consumers listing chains.
   Consider `json:"-"` or a dashboard-only serialization.
6. **Sweep-limits adapter-key drift** — `limits.go:23` defaults map uses key
   `"sol"` but `models.AdapterTypeSolana = "solana"`. The unknown-adapter branch
   silently skips the cap. Align when re-enabling Solana.
7. **DRY: `broadcast*Leg` pair** — `executor.broadcastSweepLeg` and
   `consolidate.broadcastConsolidateLeg` are near-duplicates differing only in
   `Origin`/`ParentTransactionID`. Collapse into one method parameterized by those
   two fields.
8. **DRY: 4th copy of MPC share decryption** — `hex.Decode × 3 + mpcpkg.DecryptShare`
   block duplicated in `withdraw/service.go`, `wallet/service.go`,
   `wallet_withdrawals_controller.go`, and `sweep/consolidate.go`. Extract to
   `wallet.DecryptShareA(passphrase)` helper or `mpcpkg.DecryptShareFromWallet`.
9. **Gas-price double-fetch race** — `sweep.executor.broadcastSweepLeg` fetches
   `eth_gasPrice` once for sizing, then `BuildTransfer` re-fetches it. Price movement
   between the two calls can cause broadcast failures. Thread the gas price through
   `TransferRequest`.
10. **Consolidate/preview/status: gas estimation in sweep service** — planner
    returns `estimated_gas_total_native = "0"`. Add actual estimation via a
    `Chain.EstimateSweepGas` helper or by summing per-leg `eth_estimateGas` in the
    planner.
11. **Webhook rate limit on `/gas-check`** — endpoint currently passes-through to
    `RefreshGasStatus`. Add a Redis-based 1/min/wallet cap before external consumers
    can abuse it.
12. **Non-EVM (SOL/BTC) full base-address adapter implementation** — a dedicated
    epic for proper signing/broadcast/PSBT/SPL support; see spec §1.3. Until then
    `sweep.Planner` returns `ErrUnsupportedChain` for those chains and withdraw
    falls back to the legacy single-address flow.
13. **`pt/wallet.json` missing older `withdraw.*` keys** — ~21 keys from the
    authenticate/confirm flow (`withdraw.from`, `withdraw.to`, `withdraw.confirmTitle`,
    etc.) are only in English. Out of scope for sweep but worth tracking.

## Integration gaps

14. **Miniredis** not in `go.sum` — `sweep.limits_test.go` currently tests the
    no-Redis paths. Add miniredis and cover the actual `SetNX`/`Incr` paths.
15. **E2E skeleton** at `front/e2e/tests/sweep-consolidate.spec.ts` is `describe.skip`'d;
    wire up to a staging DB + seeded fixture to enable.
16. **UserSeeder fails during `migrate-fresh --seed`** — pre-existing "preferences
    violates not-null" error blocks end-to-end seed runs. Unrelated to sweep but
    blocks Task 33's full verification.
17. **`migrate-fresh` doesn't drop Postgres enums** — a manual `DROP TYPE ... CASCADE`
    is needed on the first run after a previous seed. Blocks reproducible test DB.
