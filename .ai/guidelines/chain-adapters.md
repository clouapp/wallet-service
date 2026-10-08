# Chain and provider adapters

> Status: TARGET. Today adapters live inside `app/services/` (`chain`,
> `ingest/providers`, `price`, `blockheight`). The registry is bound by
> `ChainServiceProvider` and filled from the chain catalog that
> `AppServiceProvider.Boot` reads.
> Migration: alignment prompt (Part 1) §3.1, §3.6, §3.8.

## Where things go

| What | Where |
|---|---|
| one adapter per chain family (EVM, Bitcoin, Solana) | `app/adapters/chain/<family>/` |
| inbound ingest providers (Alchemy, Helius, QuickNode) | `app/adapters/ingest/<provider>/` |
| price providers (CoinGecko, CMC, CoinAPI, websocket) | `app/adapters/price/<provider>/` |
| block height providers | `app/adapters/blockheight/<provider>/` |
| AWS (SQS, Secrets Manager) | `app/adapters/<service>/` |
| shared HTTP transport | `app/adapters/internal/httpclient` |

The **contract an adapter satisfies is declared by its consumer** (a port in the
service package, or `pkg/types.Chain` while it is shared by several services).
`tests/architecture` allows `adapters → services` (to implement ports and map
to service sentinels) and refuses `services → adapters`.

## The registry

`chain.Registry` (chain id → adapter) is composition, not domain logic. It is
built from the `chains` / `chain_networks` / RPC URL rows by a
`ChainRegistryService` with a cache and an explicit refresh — **never by a
database query inside a provider's `Register`**. A chain unknown to the
registry is `chainregistry.ErrUnknownChain`, matched with `errors.Is`.

## Rules

- **An adapter is the only code that talks to its system.** No `net/http`,
  `ethclient`, `rpcclient` or provider SDK outside `app/adapters`.
- **RPC URLs are secrets**: stored encrypted (`facades.Crypt()`), decrypted only
  when the adapter is built, never logged (the redactor strips them; adapters
  also strip the URL from `*url.Error` before returning).
- An adapter returns **typed errors** the service can map: provider
  unavailable/timeout (→ 502/retry), not found, invalid address, insufficient
  funds, nonce/fee errors — never a raw provider message for the client.
- Every outbound call takes `ctx` and has a timeout; retries are explicit and
  bounded.
- Inbound provider webhooks are authenticated by the provider's signature in
  middleware before parsing; the handler hands a typed event to the ingest
  service.
- Amounts cross adapters as base-unit integers/strings (`pkg/amount`), never
  floats; each chain's decimals come from the token/currency rows.

## Tests

- Each adapter is tested against what it talks to: an `httptest.Server` / fake
  node (`FakeEVMNode`) — method, path, auth, body, and error mapping.
- Services are tested with the port mocked, never with a real adapter.
- Testnet e2e for signing/broadcast paths (see `custody-and-signing.md`).
