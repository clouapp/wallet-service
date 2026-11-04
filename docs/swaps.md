# Real cryptocurrency swaps

Implementation branch: `feat/swap-provider-routing`. New swaps are disabled by default.
This change is not a production rollout. Live provider acceptance, testnet custody
validation, and a funded Cloubet reservation/settlement run remain outstanding.

## Routing policy

The platform admin configures **Settings → Platform → Swaps**. The first release
accepts BTC, ETH, SOL, LTC, XRP, BNB, TRX, POL, USDT and USDC from the active custody
catalog. An asset is identified by its network and contract, not its symbol alone.

The router resolves a source-network override, then the source-currency preference,
then the global preference. SwapKit and LI.FI have independent enable switches and
sealed API credentials. SwapKit requires a key; LI.FI can be configured without one
where its public API permits access. API hosts are fixed in the composition root.

Fallback is permitted only while quoting, for no route, an unsupported route, or
provider unavailability. Authentication, policy rejections and invalid quotes fail
closed. Accepting a quote pins its provider, route, input, destination and minimum.
Changing administrative preferences never reroutes an accepted order.

## API

Both surfaces use the same handlers and resources:

| Method | Dashboard path | External API path |
| --- | --- | --- |
| POST | `/v1/wallets/{source}/swaps/quotes/to/{destination}` | `/api/v1/wallets/{source}/swaps/quotes/to/{destination}` |
| POST | `/v1/wallets/{source}/swaps` | `/api/v1/wallets/{source}/swaps` |
| GET | `/v1/wallets/{source}/swaps/{order}` | `/api/v1/wallets/{source}/swaps/{order}` |
| GET | `/v1/wallets/{source}/swaps/by-key/{key}` | `/api/v1/wallets/{source}/swaps/by-key/{key}` |

Quote body: `{"from_asset":"ETH","to_asset":"BTC","amount":"0.1"}`.
The amount is an exact decimal string. The response includes the raw and display
amounts, provider, minimum output, expiry, fees and quote routing attempts. Opaque
provider payloads and credentials never appear in HTTP responses.

Acceptance body: `{"quote_id":"<uuid>","idempotency_key":"<uuid>","passphrase":"<ephemeral>","totp_code":"<session 2FA>"}`.
Both wallet ownership and spending permissions are enforced. API callers need
`withdrawals.create`, for quotes too (a quote is a paid provider call and a stored
row); session callers need the existing withdrawal fund action, for quotes too, and
2FA. Quotes are limited to 12 per minute per source wallet (`429`). Frozen wallets
and wallets requiring multiple approvals are refused. Refusals: wrong passphrase or
code `401 invalid_swap_authorization`; 2FA not enabled `403 two_factor_required`;
passphrase attempt limiter `429 too_many_requests`; a concurrent transition `409
swap_conflict`.

Keep the same idempotency key on a timeout. A unique database constraint also
prevents spending the same quote with a different key. Replaying a funding order
returns its state without signing again. Preparatory approval continuations require
the original actor, a short authorization lifetime and the wallet passphrase.

## Execution and confirmation

```mermaid
flowchart LR
    Q[Immutable quote] --> A[Authorize and claim order]
    A --> B[Build accepted provider route]
    B --> P[Persist funding instruction]
    P --> S[MPC signing inside custody]
    S --> D[Persist signed transaction and hash atomically]
    D --> N[Broadcast exact signed bytes]
    N --> T[Track pinned provider]
    T --> C[Verify finalized deposit in scanner ledger]
    C --> O[Complete with actual credited amount]
```

Custody shares the withdrawal and wallet-operation locks. The quote sender is the
wallet's base address; fund or consolidate that address before requesting a swap.
The executor does not silently choose a child address for a sender-bound route.

EVM calls are simulated and priced by the registered RPC, which supplies nonce and
gas (at most 3M gas per call). A native call must carry exactly the accepted input value; a token call
cannot attach an additional native value. Token allowances are limited to the exact input, with a zero reset where
necessary. Each approval is a separate durable step; funding waits for successful
inclusion, inside the same execute request (up to 20 s, bounded by the quote's
expiry and the request deadline), so one authorization approves and funds before a
60 s quote lapses. An approval not visible yet is waited for, never sent twice.
Approval or funding bytes are sealed and recorded before broadcasting.
Approval journal entries identify the token contract actually called, rather
than the router receiving the later funding call.

The custody guard (`swapcustody.guardEVMCall`) bounds what a provider-built call
can take from the wallet to the quoted input, so a compromised or wrong provider
can at worst misroute that input, never another balance:

- LI.FI calls only its Diamond (`0x1231DEB6f5749EF6cE6943a275A1D3E7486F4EaE` on
  Ethereum, BSC, Polygon, Base and Arbitrum) and approves only it;
- a call never targets a catalog token contract, except the source token's exact
  `transfer(depositAddress, input)` (deposit-address routes, funded without approval);
- calldata never starts with a function that moves or authorizes balances
  (`transfer`, `approve`, `transferFrom`, `increaseAllowance`, `setApprovalForAll`,
  EIP-2612/DAI `permit`, ERC-721/1155 transfers, Permit2 approvals and transfers);
- a token input is approved only to the contract being called;
- a THORChain/Maya router `depositWithExpiry` carries the source asset, the exact
  input and a swap memo paying the quote's destination (and refunding, when named,
  its source) in the quote's destination asset;
- the target is never a contract the wallet holds a balance of (a token outside
  the catalog included: `balanceOf(wallet) > 0` refuses it);
- a standing allowance from the wallet to the target on any other catalog token
  (left by an unfinished earlier order) is revoked first, `approve(target, 0)`, one
  durable `approval_revoke` step per token, before anything is approved or funded.

Residual exposure the guard cannot remove: a provider can still misroute the
quoted input itself (a router deposit's vault, a deposit address), and an exact
approval of an order that is abandoned before funding stays until the wallet's
next swap through that spender revokes it.

Solana messages require the base wallet as sole signer and fee payer, and invoke
only allowlisted programs at the top level (system, compute budget, SPL Token,
Token-2022, associated token accounts, memo, Jupiter v6, DFlow and the LI.FI
program, from the routes LI.FI returned on 2026-11-04): the wallet's signature
reaches every program an instruction invokes, so an unknown program is refused
before simulation. The registered
RPC simulates the frozen message and checks every referenced account, including
lookup-table accounts. Native fees/rent are bounded; unrelated token debits and
changes to token ownership or delegation are refused. Existing Token-2022 source
accounts that require extensions outside this verifier are not supported.

BTC/LTC native deposits support a zero-value OP_RETURN memo up to 80 bytes and
include its size in the network fee. A memo must be a THORChain/Maya swap memo
(`=`, `s` or `SWAP`) paying the quote's destination address in the quote's
destination asset (`CHAIN.SYMBOL[-CONTRACT]`, or the `b`, `e`, `l` aliases); any
other memo is refused before signing. XRP payments never carry a memo: a SwapKit
RIPPLE transaction with `Memos` is refused at build. XRP uses native payments and destination tags.
TRON routes can execute ordinary native/TRC-20 deposits after checking their actual
transaction semantics. Arbitrary TRON contract calls, protocol memos without a
verified builder, multi-signer messages and provider `nextActions` are refused.
Provider support and liquidity still determine which particular pairs are available.
Routes custody could never fund are not quoted: LI.FI only for EVM and Solana
sources, SwapKit THORChain/Maya routes not for XRP or TRON sources, and a SwapKit
transaction family that does not match the source chain is refused at build.

An unknown broadcast remains `reconciling`, retaining its original hash. It never
causes a provider fallback or an assumed refund. Unfunded expired preparations end
as `expired` (from execute or from a refresh alike); funded orders do not expire
when their quote does.

The local workers reconcile open orders on the confirmation interval
(`swap.Service.ReconcileSwaps`, the least recently checked first), exactly as a
caller's `GET` would, so nothing waits on a client poll; in Lambda mode the
`confirmation_tracker` invocation runs the same pass. A send a node accepted
records `broadcast_outcome: accepted` and drops its sealed bytes, as a withdrawal
does, so only a send that was never answered is ever sent again. Each pass first settles the
funding transaction (`swapcustody.SettleFunding`): bytes recorded before a send that
was never answered (a crash between persisting and broadcasting) are sent again,
the same bytes, never re-signed (`sweep.SettleUnanswered`). A funding that reverted,
or that the confirmation tracker proved can never land, ends the order `failed`
(`funding_failed_on_chain`): the input never left the wallet. A provider failure or
partial delivery after funding keeps `reconciling` and names it
(`provider_reported_failure`, `provider_reported_partial`) until a refund or the
delivery is verified on chain; those orders need operator review when neither ever
arrives. LI.FI `DONE/PARTIAL` (another token delivered) is never credited as completed.

`GET` refreshes provider status. `completed` requires an independently finalized
deposit for the exact destination wallet/address/asset. The amount comes from that
ledger, not from the provider estimate. The provider's actual per-order payout is
capped by the independently confirmed ledger total: a report above what the payout
still holds (a rounded decimal) is credited at what it holds, never refused forever.
Only deposits recorded after the order was accepted count, and a 32-byte hex payout
hash is spelled one way (lower case, `0x` kept when present: EVM, BTC, LTC, XRP,
TRON) so two spellings never hold two allocations. A durable allocation under a
transaction-level lock prevents multiple orders from consuming the same units of
a batched payout. Replays retain their first allocation. Below-minimum receipts become `partial`.
Refunds likewise require an actual confirmed source-asset credit and expose its
actual amount. Provider errors after funding do not release or refund funds.

## Integration boundary and remaining validation

The companion Cloubet `feat/real-crypto-swaps` changes persist the accepted quote
and original execution key, reserve the user's source balance atomically and
reconcile from a durable job. The destination credit and ledger write commit
together, once. Unfunded expired/failed reservations are released once; refunds
credit only their actual confirmed amount. Uncertain orders retain their
reservation. Enabling Cloubet's `RealCryptoSwaps` feature routes its wallet and
market interfaces through quote/review/accept/poll and blocks the legacy
synchronous mutation. The feature starts disabled.

The dashboard recording in the sibling frontend is a controlled demonstration:
provider APIs and chain confirmations are simulated and no real funds move. Unit
tests cover routing, custody persistence before send, replay, source-asset bounds,
and independent credit verification. Repository tests run against isolated
PostgreSQL/Redis containers. A real testnet custody run is required before merging
the signing changes; a live provider credential and an agreed funded route are
needed for final provider acceptance. Provider adapters currently reject testnets;
testnet custody validation and a provider's real mainnet acceptance are separate checks.

The opt-in `TestSwapControlledHTTPDemo` fixture joins the actual Go service,
provider adapters, SQL repositories and credit verifier with Cloubet's actual PHP
service and financial ledger over HTTP. Its provider endpoints and custody
executor are synthetic, and chain credit is inserted explicitly by the test.
It uses disposable `vault_unit_test*` data only. This proves the software
integration and reservation/settlement behavior, not a real blockchain swap.

On 2026-11-01 the production LI.FI adapter also passed a read-only live quote/build
check for 0.001 ETH to USDC on Ethereum. The executable native value matched the
requested input, and output bounds/fees were parsed in exact units. Synthetic
sender/receiver addresses were used; no signing, funding or confirmation was
exercised. The HTTP client identifies itself as `MacroWallets-Swap/1.0`; the
anonymous default client signature was rejected by the provider's edge filter.
LI.FI's [public API limits](https://help.li.fi/hc/en-us/articles/12111455848859-What-is-the-LI-FI-API-rate-limit)
permit unauthenticated quote checks. This does not replace an actual funded swap.

Reproduce only the read-only provider check (no database, custody credentials or
funds needed; skipped in ordinary unit runs):

```sh
SWAP_LIVE_QUOTE_CHECK=1 go test ./app/adapters/swap -run '^TestLiveLiFi_ReadOnlyQuoteAndBuild$' -count=1 -v
```

On 2026-11-02 the same production LI.FI adapter also passed with an explicitly
supplied temporary API key and public wallet for 0.01 ETH to USDC. The unsigned
transaction's Ethereum chain ID, exact native value, source identity, output
bounds and nonempty calldata passed validation. LI.FI returns transaction data
with the quote; its adapter builds from that frozen response without re-quoting.
No custody was loaded and no transaction was signed or broadcast.

For that authenticated read-only check, supply `LIFI_API_KEY` securely through
the process environment, `LIFI_LIVE_SOURCE_ADDRESS` as the agreed public Ethereum
wallet (also used as the USDC recipient), and `LIFI_LIVE_AMOUNT_RAW` as the exact
native input units. If omitted, the test retains its unauthenticated synthetic
address check with 0.001 ETH. Never write the temporary key into source or logs.

On 2026-11-02 an authenticated read-only SwapKit check returned four ETH-to-USDC
routes for 0.01 ETH on Ethereum. The live response exposed two format differences:
expiration is a Unix timestamp string and estimated durations may be fractional.
The adapter now accepts Unix and legacy RFC3339 expiration values, and rounds a
fractional duration up to whole display seconds without changing monetary units.

The initial unsigned build was rejected with `insufficientBalance` because the
synthetic source address had no funds. A subsequent run with an explicitly
supplied public wallet passed both the production adapter's authenticated quote
and unsigned EVM build. The chain ID and native value matched Ethereum and exactly
0.01 ETH, and transaction calldata was present. The same address was used to
receive USDC. Balance checks remained enabled; no signature or broadcast occurred.
This still does not establish custody signing or a settled swap.

The opt-in test skips its build subtest unless an operator explicitly supplies a
funded public source address:

```sh
# Supply SWAPKIT_API_KEY securely in the process environment, never in source.
SWAPKIT_LIVE_QUOTE_CHECK=1 go test ./app/adapters/swap -run '^TestLiveSwapKit_ReadOnlyQuoteAndBuild$' -count=1 -v
```

Set `SWAPKIT_LIVE_SOURCE_ADDRESS` only to the agreed Ethereum mainnet wallet for
the optional unsigned-build check. Keep the temporary key out of files and logs;
configure the replacement credential through the sealed admin setting after testing.

Provider contracts: [SwapKit](https://docs.swapkit.dev/),
[LI.FI](https://docs.li.fi/). Solana checks use the documented
[simulation](https://solana.com/docs/rpc/http/simulatetransaction) and
[account snapshot](https://solana.com/docs/rpc/http/getmultipleaccounts) RPCs.
