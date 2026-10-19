# Chain and provider adapters

> Status: PARTLY HOLDS. The adapters for ingest providers, price, block height,
> AWS and the EVM, Bitcoin, Solana, TRON and XRP clients live in `app/adapters/`;
> `tests/architecture` allows `adapters → services` and refuses `services →
> adapters`. TARGET: the chain-family code that is still in `app/services/chain`
> (the registry, JSON-RPC and fee helpers) and in `app/services/ingest` moves
> behind ports as well. The registry is bound by `ChainServiceProvider` and
> filled from the chain catalog that `AppServiceProvider.Boot` reads.

## Where things go

| What | Where |
|---|---|
| one adapter per chain family (EVM, Bitcoin, Solana, TRON, XRP) | `app/adapters/chain/<family>/` (`rpc` is the shared JSON-RPC transport) |
| inbound ingest providers (Alchemy, Helius, QuickNode) | `app/adapters/ingest/<provider>/` |
| price providers (CoinGecko, CMC, CoinAPI, websocket) | `app/adapters/price/<provider>/` |
| block height providers | `app/adapters/blockheight/<provider>/` |
| AWS (SQS, Secrets Manager), Redis stores, webhook delivery | `app/adapters/queue/sqs`, `app/adapters/secretsmanager`, `app/adapters/redis/*`, `app/adapters/webhook` |
| shared HTTP transport | `pkg/httpclient` |

The **contract an adapter satisfies is declared by its consumer** (a port in the
service package, or `pkg/types.Chain` while it is shared by several services).
`tests/architecture` allows `adapters → services` (to implement ports and map
to service sentinels) and refuses `services → adapters`.

## The registry

`chain.Registry` (`app/services/chain`, chain id → adapter) is composition, not
domain logic. It is built from the `chains` rows (with their sealed RPC URL) by
`chainregistry.ChainRegistryService`, with a cache and an explicit refresh —
**never by a database query inside a provider's `Register`**. A chain unknown to the
registry is `chainregistry.ErrUnknownChain`, matched with `errors.Is`.

## Rules

- **An adapter is the only code that talks to its system.** No `net/http`,
  `ethclient`, `rpcclient` or provider SDK outside `app/adapters`.
- **RPC URLs are secrets**: stored encrypted (`facades.Crypt()`), decrypted only
  when the adapter is built, never logged (the redactor strips them; adapters
  also strip the URL from `*url.Error` before returning). Logs, health records
  and operator output name an endpoint by its host (`chain.RPCEndpointHost`) and
  fingerprint (`chain.RPCEndpointFingerprint`) only.
- An adapter returns **typed errors** the service can map: provider
  unavailable/timeout (→ 502/retry), not found, invalid address, insufficient
  funds, nonce/fee errors — never a raw provider message for the client.
- Every outbound call takes `ctx` and has a timeout; retries are explicit and
  bounded.
- **A chain's endpoints are an ordered list**: `chains.rpc_url` (position 1) and
  `chain_rpc_endpoints` (the fallbacks). EVM and Solana adapters get the opened
  fallbacks in their config and pass them to `rpc.RPCClientDeps`, with an identity
  check (`chain.CheckEVMNetwork`, `chain.CheckSolanaGenesis`) a fallback must pass
  before its first use. No provider URL is hard-coded in an adapter: proposed
  lists live in `app/services/rpcendpoints/defaults.go`.
- What moves a call to the next endpoint is decided once, in the RPC client
  (`app/adapters/chain/rpc/classify.go`). An adapter never retries a call on
  another endpoint itself; a new "this node cannot serve it" error is added
  there, with a test. Reads that must come from one node (a block, then its logs)
  share `rpc.PinEndpoint(ctx)`. A broadcast never moves on once an endpoint may
  have accepted it; a later "already known" is `rpc.ErrBroadcastAlreadyKnown`,
  which the adapter answers with the locally computed hash.
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
