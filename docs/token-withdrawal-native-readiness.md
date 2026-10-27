# EVM token withdrawals: sender gas readiness

An address holding enough tokens cannot send them without the chain's native
fee asset. Planning must examine the actual sender, independently of the base
wallet's cached gas status. This applies to every EVM token and its native fee
asset; it does not special-case a token symbol or network name.

The planner retains `direct_from_child` when that child can pay the transfer's
native fee. It uses the adapter's estimated gas limit, wallet fee policy, gas
price and applicable rollup L1 data fee. An unavailable, zero or invalid estimate
does not become a zero-cost plan. A native-balance read must also succeed.
Without a recipient, the existing declared EVM fee probe checks readiness;
`EstimatedGas` remains unknown for the actual recipient.

An unfunded child may use the existing `multi_sweep` preparation only when its
entire base funding budget is measurable and payable. The budget includes each
required gas seed, the same 120% buffer used by `BuildSweep`, each seed's fee and
the final base-to-recipient transfer's fee. The base must pay the whole budget.
Native transfers and other adapters keep their existing fee rules.

Execution repeats these reads before retrieving the service share or signing.
The direct sender is checked again before its transfer is built and signed.
Preparation awaits the existing seed-inclusion step, then checks the child's
observed native balance before signing the token sweep. A stale plan or an
included seed without available funds cannot authorize a child signature.

## Current conservative boundary

A base with no tokens may be unable to simulate the final token transfer before
the sweep arrives. This guard refuses that plan with the typed gas-estimate
error instead of inventing the base's future state or simulating a different
sender as if it were the base. It is a safety fix, not completion of the
zero-token-base preparation scenario. A separate resumable direct-child gas
preparation design is needed to preserve the original sender in that case.

This change neither retries nor releases any existing failed withdrawal,
idempotency key, owner intent or central funding fence. Recovery of an existing
operation requires separate reconciliation and review.

## Validation

`token_gas_readiness_test.go` covers a funded direct sender, one unit below its
fee, a zero-native child, direct base readiness, the complete preparation budget,
missing base gas, balance/fee/L1 RPC errors, an unestimable zero-token base,
funding lost after planning or before signing, the existing seed/sweep/final
pipeline, and a seed observed in a block without the child's funds being
available. Tests assert no key retrieval, signing, rows or broadcast on initial
readiness refusal. All tests use synthetic adapters and wallets.
