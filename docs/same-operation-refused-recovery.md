# Same-operation refused EVM withdrawal recovery

This service is an explicit test-environment recovery path. It does not call
`withdraw.Create`, `RetryBroadcast`, the normal withdrawal planner, or the generic
same-key POST. It never enrolls or reopens a Laravel funding lane.

The input is the existing business withdrawal UUID and its original provider
transaction. A server-derived binding fixes the account, wallet, child address
and derivation, network, original nonce (including zero), asset, token contract,
precision, raw amount, recipient, finality policy, request digest and provider
fingerprint. Only allowlisted EVM testnets and active test accounts qualify.

## Inspect, review and apply

`Inspect` reads the original binding and current chain facts, and produces a
bounded proposal. It never reserves a nonce, seeds, retrieves keys, signs, or
sends. Missing receipt/transaction data is not proof of a final failed payment.
Both RPC nonce observations must still equal the exact original nonce, neither
original nor candidate may be visible, and provider/account/network facts must
agree. A known original, known candidate, receipt, changed nonce or disagreement
stops the replacement and leaves the operation occupied for reconciliation.

`Apply` requires an unexpired independent review of that exact proposal and code
revision. The prior first-enrollment review cannot be reused. The review bounds
gas limit, gas price and total native fee. Any gas prerequisite names its base
address and exact sender nonce, amount and fee cap separately.

Migration 700 provides a unique business-operation claim and a unique
`(network_id, sender, sender_nonce)` reservation. Economic and review fields,
frozen call, gas proof, and persisted signed candidate cannot be rewritten.
Every execution transition has an append-only event. Repository writes own and
commit their physical transaction; an ambient outer transaction is refused.

## Gas and signatures

A gas prerequisite is claimed before invoking its preparation adapter. That
adapter must have its own signed-before-send journal keyed by this attempt.
Observation remains read-only. Explicit `Resume` may invoke the optional gas
continuation port only after fresh original/hash/nonce checks and while the
stored review remains valid. Under its physical writer lock, that port may
create a missing exact step only when durable-before-send ordering proves no
step was sent, or replay an existing step's exact sealed bytes. RPC absence or
an observation error alone never proves a step is missing. Continuation cannot
change the reviewed seed amount, nonce, child, operation or authorization claim.
After seed confirmation, the original hash and nonce are checked again: adding
gas can make the original signed transaction valid.

The replacement call is frozen before signing. Its calldata is exactly ERC-20
`transfer(original_recipient, original_raw_amount)` with native value zero. The
signer receives the original child ID, network and exact nonce. Recovery then
independently decodes the signed transaction and recovers its signer, checking
all economics, nonce and gas fields. It seals those verified bytes before any
network call.

The new `transactions` row is an execution attempt, not another payment UUID.
Its amount remains raw base units (for example `6100000`, not `6.1`). It uses the
same business idempotency key and records the predecessor both as a typed
`parent_transaction_id` relation and as
`raw_payload.recovery_predecessor_transaction_id`. The original failed row is
preserved. Candidate creation, sealed hash/bytes persistence, append-only event,
and CAS of `withdrawals.transaction_id` from the expected predecessor commit
atomically before send. A failure rolls the whole transaction back.

The withdrawal database guard blocks ordinary retry while this attempt is
occupied, changing its economic fields or pointing it back at the predecessor.
A predecessor cannot fail or confirm a pending candidate. The lifecycle
publisher additionally checks the current transaction reference, including the
recovery-predecessor relation after the old idempotency key has been released.
Known predecessor callbacks are no-ops, without orphan events. Repository outcome
updates CAS the current transaction ID (or a NULL legacy link), so an old failure
cannot overwrite a failed candidate's reason. Physical confirmation publication
commits that CAS and its staged outbox atomically and queues only after commit;
a pointer race or outbox failure cannot leak an old confirmation. Failure staging
returns a CAS race to the caller so its outer transaction rolls back the outbox.

## Interruptions and remaining uncertainty

An explicit `Resume` finds the existing attempt. Before a durable candidate it
can sign only its frozen call, never plan another source or nonce. A candidate
CAS selects one winner. After a durable candidate, `Resume` can only observe or
replay the exact sealed bytes. A send interruption or unknown answer retains the
candidate and nonce reservation. Repeating identical signed bytes is the same
transaction hash, not a second economic payment; it does not assume the earlier
send failed. Native gas is checked again immediately before transmission.

An original/candidate appearing, a consumed nonce, or a receipt is a stop for
independent canonical-chain reconciliation. `needs_reconciliation` does not
mean success, no debit, a refunded customer, or permission to increment the
nonce. The service deliberately provides no release/unfence operation and does
not conclude finality from provider labels. A new deployment or expired review
requires a new explicitly reviewed operator decision; read-only lookup remains
available.

## Verification

Domain tests use real EVM signatures but no real keys, nodes or funds. They cover
nonce zero, immutable fields, exact signed semantics, independent review,
concurrent claims, durable-before-send failures, sealed-byte replay, original
visibility during gas funding and drained native gas before transmission.
Repository tests run against disposable PostgreSQL worker clones with
`TEST_DB_REQUIRED=1`; they exercise physical commits, unique claims, immutable
triggers/events, CAS linkage, failure rollback, ordinary-retry refusal and stale
predecessor status protection. These tests do not authorize a runtime recovery.
