# Resumable direct-child gas preparation

An EVM child that already holds the full token withdrawal amount stays the
sender. Its missing native gas is prepared with one base-to-child transfer;
tokens are not consolidated into an empty base as a workaround. The withdrawal
preview includes `gas_preparation`, its funder, child, native amount, fee bound,
remaining funder reserve and required confirmation. It remains a
`direct_from_child` plan with zero token sweeps.

## Quote and immutable instruction

The quote simulates the actual child-to-recipient token transfer and the actual
native base-to-child seed. Both use the adapter's gas limit, wallet fee policy,
gas price and exact gas limit passed into the L1 fee estimate. The seed uses the
shared EVM 120% buffer. The base must retain the larger configured chain/adapter
gas-readiness reserve after paying the seed and its fee. Failed or unavailable
balance and fee observations refuse preparation.

`DirectChildGasBinding` fixes operation, authorization claim, account, wallet,
child ID/address/index/external user, chain/network, token contract/decimals,
raw amount and recipient. A recovery may additionally bind the original child
nonce and original transaction ID. `DirectChildGasPlan` fixes the base,
optional exact base nonce (zero is valid), seed amount, fee ceiling, reserve,
token fee budget and expiry. An expired quote cannot create a new signature.

The step UUID is derived from operation UUID and purpose, **not** from child
ID. Changing child, recipient or amount for the same operation therefore cannot
create a second seed. `OperationPlanner` checks for an existing step before
strategy selection; a retry stays pinned to its committed binding even if
another child becomes funded.

## Commit, send and resume

The existing `transactions` table stores one `gas_seed` row with the stable
step UUID/idempotency key, exact sender/recipient/amount, operation binding,
signed fee budget, signed hash, nonce and sealed signed bytes. An original
transaction, when supplied, is also its parent. Normal operations retain their
business operation UUID in the immutable payload; their final withdrawal keeps
its original business idempotency key.

The repository refuses ambient outer transactions, verifies a writable
PostgreSQL transaction, and locks wallet then step with ordered advisory locks.
Wallet state is refreshed while its row is locked. Signing and the immutable
insert happen under that transaction. A different already-recorded outbound
transfer owning the base nonce blocks the new signature. A pre-commit failure
rolls back and sends nothing. The transaction physically commits before any
broadcast or inclusion wait; network transmission never holds that SQL lock.

After commit, only the recorded bytes may leave. A lost response or process
failure never authorizes a new signature, another seed value, another nonce or
another source. `ResumeDirectChildGas` reuses the exact committed bytes and
checks the current funder reserve and fee envelope. It cannot widen the old
instruction. An existing included seed is observed, never transmitted again,
even if the child's gas later becomes insufficient.

`ObserveDirectChildGas` performs only reads. Missing storage uses
`models.ErrRepositoryNotFound`; corrupt bindings, missing bytes and an
unresolved receipt are different outcomes and do not authorize a fresh step.
`Confirmed` means successful receipt inclusion plus currently sufficient native
gas at the child; it is not a claim of finalized-block status. `Included` is
reported separately. No token signature occurs while preparation is unknown
or the child is still unfunded. Normal withdrawal execution checks the sender
again immediately before its token signature.

The confirmation tracker retains sealed bytes for this purpose-tagged seed
after inclusion or failure so the observer can still verify its exact economics.
Ordinary withdrawal byte cleanup is unchanged. Sealed payloads are never part
of HTTP resources. `FeeWei` in the preparation result is the signed fee budget,
including the quoted L1 component; actual paid fees remain the confirmation
tracker's transaction ledger data.

## Recovery integration

The exported `DirectChildGasPreparer` is an optional port on the existing sweep
service: Quote, Prepare, read-only Observe, and exact-bytes Resume. Recovery's
own durable claim and independent review must precede Prepare. A wrapper must
read and pin the base nonce explicitly when its reviewed plan requires one;
the builder's nonce and signed RLP are both checked against it. The recovery
observer remains responsible for checking the original child nonce and original
payment before and after funding. This preparer never signs that token recovery
payment or changes its sender.

No existing refused withdrawal, owner intent or central funding fence is reset,
closed or retried by installing this feature.

## Validation

Tests cover actual EVM signatures, a normal operation producing exactly seed
plus original-child payment, twelve concurrent preparers, physical commit and
rollback, lost commit acknowledgment, same-byte resume, zero-nonce ownership,
binding/fee/expiry drift, failed sealing/inserts, stale funder balance, included
seed without observed child funds, and null nonce rejection. Disposable
PostgreSQL tests verify repository locking, outer-transaction refusal and
concurrent uniqueness. A real repository-backed confirmation-tracker test then
calls Observe and Resume after inclusion and asserts no second network send.
