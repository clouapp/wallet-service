# Webhook-Based Deposit Monitoring — Design Spec

**Date:** 2026-03-27
**Status:** Approved (pending implementation plan)
**Reviewed:** Spec review v2 — all critical/important issues resolved

## Overview

Replace RPC-polling block scanning with provider webhook push notifications for deposit detection. Alchemy (EVM chains), Helius (Solana), and QuickNode Streams (Bitcoin) push transaction events to our ingest endpoint. A lightweight confirmation tracker uses free block explorer APIs for block height. Address lists sync to providers automatically on wallet create/deactivate.

## Goals

1. Eliminate per-block RPC scanning (the primary cost driver)
2. Detect deposits in near-real-time via provider webhooks instead of periodic Lambda polling
3. Track confirmations using free block explorer APIs (Etherscan, Blockstream, Solana public RPC)
4. Automatically sync wallet addresses to provider subscriptions on create/deactivate
5. Maintain existing deposit processing logic (processTransfer, webhook enqueue, status transitions)
6. Keep RPC usage only for on-demand operations (balance checks, tx broadcasting)

## Non-Goals

- Changing the withdrawal flow (still uses RPC for BuildTransfer/Sign/Broadcast)
- Running self-hosted blockchain nodes
- Replacing the internal webhook delivery system (SQS → customer endpoints)

---

## 1. Provider Mapping

| Chain | Provider | Product | Address Limit | Signature Verification |
|-------|----------|---------|---------------|----------------------|
| ETH, Polygon (all EVM) | Alchemy | Address Activity Webhook | 100K per webhook, 5 webhooks free tier | HMAC-SHA256 via `X-Alchemy-Signature` header |
| Solana | Helius | Transaction Webhook | 100K per webhook | Custom `authHeader` (bearer token set at creation) |
| Bitcoin | QuickNode | Streams (with JS filter) | Embedded in filter function | HMAC verification + IP allowlist (`141.148.40.227`) |

### Provider API Reference

**Alchemy (EVM):**
- Create: `POST https://dashboard.alchemy.com/api/create-webhook`
- Update addresses: `PATCH https://dashboard.alchemy.com/api/update-webhook-addresses` (idempotent, takes `webhook_id`, `addresses_to_add`, `addresses_to_remove`)
- List addresses: `GET https://dashboard.alchemy.com/api/webhook-addresses?webhook_id=ID&limit=100&after=CURSOR` (paginated, used by Alchemy adapter for diffing during SyncAddresses)
- Delete: `DELETE https://dashboard.alchemy.com/api/delete-webhook` (takes `webhook_id`)
- Auth header: `X-Alchemy-Token`
- `webhook_type`: `ADDRESS_ACTIVITY`
- Networks: `ETH_MAINNET`, `ETH_SEPOLIA`, `MATIC_MAINNET`, `MATIC_MUMBAI`, `ARB_MAINNET`, `BASE_MAINNET`, `OPT_MAINNET`, + 30 more EVM chains
- Note: Verify `removed: true` reorg flag behavior against current Alchemy Notify docs before implementation — payload shape may differ by network/version

**Helius (Solana):**
- Create: `POST https://api-mainnet.helius-rpc.com/v0/webhooks?api-key=KEY`
- Update: `PUT https://api-mainnet.helius-rpc.com/v0/webhooks/{webhookID}?api-key=KEY` (full body replacement including `accountAddresses`)
- `webhookType`: `enhanced` (mainnet), `enhancedDevnet` (devnet)
- Cost: 100 credits per API call, 1 credit per event delivered

**QuickNode (Bitcoin):**
- Create stream: `POST https://api.quicknode.com/streams/rest/v1/streams`
- Update stream: `PATCH https://api.quicknode.com/streams/rest/v1/streams/{id}`
- Auth header: `x-api-key` (with `STREAMS_REST` permission)
- `network`: `bitcoin-mainnet`
- `dataset`: `block`
- `filter_function`: base64-encoded JavaScript that filters for tracked addresses
- Cost: 30 API credits per payload delivered

---

## 2. Webhook Ingest Endpoint

### Route

```
POST /v1/webhooks/ingest/:provider/:chainID
```

Examples:
- `POST /v1/webhooks/ingest/alchemy/eth`
- `POST /v1/webhooks/ingest/alchemy/polygon`
- `POST /v1/webhooks/ingest/helius/sol`
- `POST /v1/webhooks/ingest/quicknode/btc`

### Flow

```
1. Read raw body bytes (do NOT re-marshal — HMAC verification requires exact bytes)
2. Look up webhook_subscription by (provider, chainID)
3. Verify signature/auth per provider:
   - Alchemy: HMAC-SHA256 of raw body against signing_key, constant-time compare to X-Alchemy-Signature header
   - Helius: Constant-time compare of Authorization header against stored authHeader value
   - QuickNode: HMAC verification of raw body + IP allowlist check (defense in depth)
4. On verification failure: return 401, log attempt with source IP. Do not process.
5. Optionally deduplicate at ingest level using provider event ID (whevt_xxx) in Redis with short TTL
6. Parse payload into []InboundTransfer (normalized)
7. For each transfer:
   a. Check Redis set vault:addresses:{chainID} — is transfer.To ours?
   b. Deduplicate by (chainID, txHash, logIndex) via txRepo.CountByChainTxHashAndLogIndex
   c. Insert deposit transaction (reuses existing processTransfer logic)
   d. Enqueue deposit.pending webhook to SQS
8. Return 200 OK (providers retry on non-2xx with exponential backoff)
```

### Security

- No API key authentication (public endpoint, provider-originated)
- Verification via provider-specific HMAC or auth header with **constant-time comparison** (see above)
- QuickNode IP allowlist (`141.148.40.227`) as **defense in depth** — not sole auth. Verify current IPs against QuickNode docs before deploy; set up alerting on verification failures.
- Idempotent: duplicate deliveries handled by composite dedup check
- Alert on signature failure spikes (potential replay attack or provider misconfiguration)

### Payload Formats

**Alchemy ADDRESS_ACTIVITY payload:**
```json
{
  "webhookId": "wh_xxx",
  "id": "whevt_xxx",
  "type": "ADDRESS_ACTIVITY",
  "event": {
    "network": "ETH_MAINNET",
    "activity": [{
      "blockNum": "0xdf34a3",
      "hash": "0x7a4a39da...",
      "fromAddress": "0x503828...",
      "toAddress": "0xbe3f4b...",
      "value": 293.092129,
      "asset": "USDC",
      "category": "token",
      "rawContract": {
        "rawValue": "0x...11783b21",
        "address": "0xa0b86991...",
        "decimals": 6
      },
      "log": {
        "logIndex": "0x6e",
        "transactionHash": "0x7a4a39da...",
        "blockHash": "0xa99ec5..."
      }
    }]
  }
}
```

**Helius enhanced payload:** Parsed Solana transaction with `type`, `source`, `fee`, `nativeTransfers[]`, `tokenTransfers[]`, and account keys. Uses `webhookType: enhanced` for human-readable data.

**QuickNode Streams payload:** Raw Bitcoin block data filtered through JS function. The filter extracts matching vout entries and returns `{txid, blockNumber, blockHash, toAddress, amount, timestamp}`.

---

## 3. Confirmation Tracker

Replaces the block-scanning portion of `updateConfirmations`. Runs as a lightweight scheduled Lambda.

### Lambda Mode

New `LAMBDA_MODE`: `confirmation_tracker`

### Schedule

Single EventBridge rule, every 60 seconds. One invocation processes all chains with pending deposits.

### Flow

```
1. Query txRepo.FindPendingByChain for each active chain
2. Skip chains with zero pending deposits
3. Fetch current block height from free API:
   - EVM chains: Etherscan V2 — GET /v2/api?chainid={id}&module=proxy&action=eth_blockNumber (100K calls/day free)
   - Bitcoin: Blockstream — GET https://blockstream.info/api/blocks/tip/height (free, no key)
   - Solana: Public RPC — getSlot with finalized commitment (free endpoint)
4. Run existing updateConfirmations logic per chain
5. Fire deposit.confirming / deposit.confirmed webhooks as status transitions
```

### BlockHeightProvider Interface

```go
type BlockHeightProvider interface {
    GetBlockHeight(ctx context.Context, chainID string) (uint64, error)
}
```

Implementations:
- `EtherscanBlockHeight` — HTTP GET, parses hex response, supports all EVM chains via `chainid` param. Validate Etherscan chain coverage per chain before deploy (Polygon Amoy, etc.).
- `BlockstreamBlockHeight` — HTTP GET, returns integer directly
- `SolanaPublicBlockHeight` — JSON-RPC `getSlot` to public endpoint `https://api.mainnet-beta.solana.com`

**Timeouts:** Each provider call has a 5-second timeout. On failure, skip that chain for this run.

**Staleness detection:** If block height has not advanced for 5+ consecutive checks (5 minutes), log a warning. This catches stuck explorers or chain halts and prevents deposits from being confirmed against stale data.

**Fallback:** If the primary block height source fails 3 consecutive times, fall back to a bounded RPC call (`GetLatestBlock`) as a degraded-mode safety net. This uses minimal RPC credits and only activates under sustained explorer outage.

**Rate limits:** Etherscan free tier allows 5 req/s without key, 100K/day with free key. With 4 chains checking every 60s, usage is ~5,760 calls/day — well within limits. Share a single Etherscan API key across chains with a simple rate limiter.

---

## 4. Webhook Subscription Management

### Database Table

```sql
CREATE TABLE webhook_subscriptions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id              VARCHAR(20) NOT NULL REFERENCES chains(id),
    provider              VARCHAR(20) NOT NULL,  -- 'alchemy', 'helius', 'quicknode'
    provider_webhook_id   VARCHAR(255) NOT NULL,
    webhook_url           TEXT NOT NULL,
    signing_secret        TEXT NOT NULL,          -- encrypted via facades.Crypt()
    status                VARCHAR(20) NOT NULL DEFAULT 'active',
    sync_status           VARCHAR(20) NOT NULL DEFAULT 'synced', -- 'synced', 'pending', 'failed'
    synced_addresses_hash VARCHAR(64),            -- SHA-256 of sorted address list, for Alchemy diffing
    last_synced_at        TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(chain_id, provider)
);
```

**`synced_addresses_hash`:** SHA-256 hash of the sorted, joined address list at last sync. Used by the Alchemy adapter to detect whether a sync is needed and by the reconciler to detect drift. The Alchemy adapter fetches the current address list from the provider API (`GET /webhook-addresses`) to compute the diff for `addresses_to_add`/`addresses_to_remove`.

**`sync_status`:** Tracks the result of the last address sync attempt. Set to `pending` before sync, `synced` on success, `failed` on error. The reconciler retries `failed` subscriptions.

### WebhookProvider Interface

```go
type WebhookProvider interface {
    ProviderName() string
    CreateWebhook(ctx context.Context, cfg ProviderConfig) (*ProviderWebhook, error)
    SyncAddresses(ctx context.Context, webhookID string, allAddresses []string) error
    DeleteWebhook(ctx context.Context, webhookID string) error
    VerifyInbound(headers http.Header, body []byte, secret string) (bool, error)
    ParsePayload(body []byte) ([]InboundTransfer, error)
}
```

`SyncAddresses` takes the **full current address list** (not deltas). Each provider implementation handles the diff internally. Calls are **serialized per subscription** (mutex or DB advisory lock) to prevent concurrent sync races.

- **Alchemy:** Fetches current addresses from provider via `GET /webhook-addresses`, computes diff, calls `addresses_to_add` / `addresses_to_remove`. Updates `synced_addresses_hash` on success.
- **Helius:** PUTs full body with complete `accountAddresses` array (their API requires full replacement).
- **QuickNode:** Re-encodes JS filter function with updated address set, PATCHes the stream.

`VerifyInbound` returns `(bool, error)` instead of just `bool` — the error carries the reason (missing header, parse failure, etc.) for logging and metrics.

### ProviderConfig

```go
type ProviderConfig struct {
    ChainID     string
    Network     string   // e.g. "ETH_MAINNET", "bitcoin-mainnet"
    WebhookURL  string
    Addresses   []string
    APIKey      string   // provider-specific auth token
    AuthSecret  string   // for Helius: bearer token; for others: derived
}
```

### ProviderWebhook (create response)

```go
type ProviderWebhook struct {
    ProviderWebhookID string
    SigningSecret      string // Alchemy: signing_key from response; Helius: authHeader; QuickNode: HMAC key
}
```

---

## 5. Address Sync on Wallet Create/Deactivate

### WebhookSyncService

Sits between wallet operations and provider adapters.

```go
type WebhookSyncService struct {
    subscriptionRepo WebhookSubscriptionRepository
    addressRepo      AddressRepository
    providers        map[string]WebhookProvider  // keyed by provider name
}
```

### On Wallet Creation

After `WalletService.CreateWallet` succeeds and the deposit address is stored:

```
1. Determine chain_id from the new wallet
2. Load webhook_subscription for that chain_id
3. Load all active addresses for that chain from addressRepo
4. Call provider.SyncAddresses(providerWebhookID, allAddresses)
5. Update last_synced_at on the subscription
6. Refresh Redis cache (existing RefreshAddressCache)
```

### On Wallet/Address Deactivation

Same flow — load all remaining active addresses, sync the full list.

### Failure Handling

- Sync failures are logged but **non-blocking** (wallet creation still succeeds)
- A `sync_status` field on the subscription tracks last sync result
- Background reconciliation job (daily) compares DB addresses against provider state

### Reconciliation Job

New `LAMBDA_MODE`: `webhook_reconciler` (EventBridge schedule, once daily)

```
1. For each active webhook_subscription:
   a. Load all active addresses from DB for that chain
   b. Call provider.SyncAddresses with the full list
   c. Update last_synced_at
2. Log any discrepancies
```

---

## 6. InboundTransfer Type

Normalized struct all providers parse into. Intentionally mirrors existing `DetectedTransfer`:

```go
type InboundTransfer struct {
    TxHash      string
    BlockNumber uint64
    BlockHash   string
    From        string
    To          string
    Amount      *big.Int
    Asset       string
    Token       *types.Token
    LogIndex    uint
    Timestamp   time.Time
}
```

Maps directly to `types.DetectedTransfer` so `processTransfer` logic remains unchanged.

### Deduplication Key

The current `txRepo.CountByChainAndTxHash` deduplicates on `(chain_id, tx_hash, tx_type)`. This is insufficient for EVM transactions that contain multiple Transfer events to different vault addresses in the same tx (e.g., a batch contract distributing tokens to two of our wallets).

**Required change:** Add a `log_index` column (INT NOT NULL, default `-1`) to the `transactions` table. Native transfers and Bitcoin use the sentinel value `-1` (no log). ERC-20 token transfers use the actual log index from the event.

Migration:
```sql
ALTER TABLE transactions ADD COLUMN log_index INT NOT NULL DEFAULT -1;
CREATE UNIQUE INDEX idx_transactions_dedup ON transactions (chain, tx_hash, log_index, tx_type);
```

Using `-1` as a sentinel (instead of NULL) ensures the PostgreSQL UNIQUE constraint works correctly — `NULL != NULL` in PostgreSQL would allow duplicate native-transfer rows, but `-1 = -1` enforces uniqueness as intended.

Extend the dedup query to `CountByChainTxHashAndLogIndex(chainID, txHash, logIndex, txType)`. The ingest handler passes `-1` for native/Bitcoin transfers and the actual log index for token transfers.

---

## 7. File Layout

```
back/app/services/ingest/
├── service.go              # IngestService — receives parsed transfers, runs processTransfer
├── handler.go              # HTTP handler for POST /v1/webhooks/ingest/:provider/:chain

back/app/services/ingest/providers/
├── provider.go             # WebhookProvider interface, InboundTransfer, ProviderConfig types
├── alchemy.go              # Alchemy: create/update, HMAC verify, parse ADDRESS_ACTIVITY payload
├── helius.go               # Helius: create/update, authHeader verify, parse enhanced payload
├── quicknode.go            # QuickNode: create/update stream, HMAC verify, parse filtered BTC payload

back/app/services/blockheight/
├── provider.go             # BlockHeightProvider interface
├── etherscan.go            # Etherscan V2 block height (all EVM chains via chainid param)
├── blockstream.go          # Blockstream REST block height (Bitcoin)
├── solana_public.go        # Solana public RPC getSlot

back/app/services/deposit/
├── service.go              # Modified — ScanLatestBlocks deprecated, processTransfer stays
├── confirmation.go         # Extracted updateConfirmations + RunConfirmationCheck entry point

back/app/services/webhooksync/
├── service.go              # WebhookSyncService — manages provider subscriptions + address sync
├── reconciler.go           # Daily reconciliation job

back/app/repositories/
├── webhook_subscription_repository.go  # CRUD for webhook_subscriptions table

back/app/models/
├── webhook_subscription.go # WebhookSubscription model

back/database/migrations/
├── XXXXXX_create_webhook_subscriptions_table.go
```

---

## 8. Configuration

### Environment Variables (new)

```bash
# Alchemy — management API auth (global, used to create/update webhooks)
ALCHEMY_AUTH_TOKEN=           # X-Alchemy-Token for Notify API (from dashboard top of webhooks page)

# Helius — management API auth (global)
HELIUS_API_KEY=               # API key for webhook management (query param)

# QuickNode — management API auth (global)
QUICKNODE_API_KEY=            # API key with STREAMS_REST permission

# Block height (free APIs)
ETHERSCAN_API_KEY=            # Optional — free tier works without key (5 req/s), with key (10 req/s)

# Ingest
WEBHOOK_INGEST_BASE_URL=     # Public base URL for ingest endpoints (e.g. https://api.vault.dev)
```

**Per-webhook secrets are stored in `webhook_subscriptions.signing_secret`** (encrypted via `facades.Crypt()`), NOT in environment variables. Each webhook gets its own signing key:
- **Alchemy:** `signing_key` returned in the create-webhook response
- **Helius:** `authHeader` value we generate and set at creation time (high-entropy random string)
- **QuickNode:** HMAC secret configured during stream creation

This separation ensures: env vars = management API auth (needed to create/modify webhooks), DB = per-webhook inbound verification secrets (needed to validate incoming payloads).

### Chain → Provider → Network Mapping

Stored in `webhook_subscriptions` table. Seeded during setup:

| chain_id | provider | network identifier |
|----------|----------|--------------------|
| eth | alchemy | ETH_MAINNET |
| polygon | alchemy | MATIC_MAINNET |
| teth | alchemy | ETH_SEPOLIA |
| tpolygon | alchemy | MATIC_MUMBAI (verify — Polygon has deprecated Mumbai in favor of Amoy; check Alchemy support) |
| sol | helius | mainnet (webhookType: enhanced) |
| tsol | helius | devnet (webhookType: enhancedDevnet) |
| btc | quicknode | bitcoin-mainnet |
| tbtc | quicknode | bitcoin-testnet |

---

## 9. Migration Path

### Phase 1: Deploy webhook ingest alongside existing scanner
- Add ingest endpoint + provider adapters
- Create webhook subscriptions at providers
- Both systems run in parallel — ingest creates deposits, scanner catches anything missed
- Monitor for 1-2 weeks

**Success criteria for Phase 1 → Phase 2 transition:**
- Webhook ingest detects >= 99% of deposits before the scanner does (measured by comparing `created_at` timestamps)
- Zero missed deposits over a 7-day window (every deposit found by scanner was also found by ingest)
- No sustained signature verification failures
- Address sync reconciliation shows zero drift for 3+ consecutive daily runs

### Phase 2: Disable block scanning
- Remove EventBridge `deposit_scanner` schedules
- Keep `ScanLatestBlocks` code as fallback (callable manually)
- Enable `confirmation_tracker` Lambda on schedule

### Phase 3: Cleanup
- Remove `GetLatestBlock` and `ScanBlock` from hot path
- Remove deposit_scanner Lambda from `template.yaml`
- Update `RPC_PROVIDERS.md` to reflect reduced RPC usage

---

## 10. Cost Impact

| Operation | Before (RPC polling) | After (webhooks) |
|-----------|---------------------|-------------------|
| Block scanning (4 chains, every 30s) | ~11,500 RPC calls/hour | **0** (providers push to us) |
| Deposit detection | ScanBlock RPC calls | **Free** (included in provider plans) |
| Confirmation tracking | GetLatestBlock RPC per chain | **Free** (Etherscan/Blockstream/public Solana) |
| Balance checks | RPC (on demand) | RPC (on demand) — **unchanged** |
| Tx broadcasting | RPC (on withdraw) | RPC (on withdraw) — **unchanged** |

Remaining RPC usage: user-triggered balance checks + withdrawal broadcasting only.

---

## 11. Error Handling

- **Provider delivery failure:** All three providers retry on non-2xx responses with exponential backoff (Alchemy: up to 10 min for free tier, 1 hour for enterprise)
- **Duplicate delivery:** Handled by composite dedup check `(chain, txHash, logIndex, txType)`
- **Signature verification failure:** Return 401, log attempt with source IP and provider. Alert on spikes (>5/min).
- **Address sync failure:** Non-blocking, logged. `sync_status` set to `failed`. Reconciliation job retries daily.
- **Block explorer API failure:** Confirmation tracker skips that chain for this run, retries next minute. After 3 consecutive failures, falls back to bounded RPC call.
- **Payload parsing failure:** Return 200 (to prevent infinite retries) but log the full body for debugging. Alert on parsing errors.

### Chain Reorg Handling

Chain reorgs require careful state management to avoid double-crediting deposits.

**Alchemy (EVM):** Sends a follow-up webhook with `removed: true` on the reorged activity entries when a block is reorganized out of the canonical chain. The handler:
1. On `removed: true`: look up the deposit by `(chain, txHash, logIndex)`.
2. If found and status is `pending` or `confirming`: set status to `failed`, emit `deposit.failed` webhook to customer.
3. If status is already `confirmed`: log critical alert — manual review required (should not happen if `requiredConfirmations` is sufficient).
4. The replacement transaction (if any) arrives as a new webhook delivery and creates a new deposit normally.

**Helius (Solana):** Solana uses the `finalized` commitment level, which means the transaction has been confirmed by a supermajority. Reorgs of finalized slots are extremely rare. The `enhanced` webhook type delivers only finalized transactions. Combined with `requiredConfirmations: 1` (finalized), reorg risk is negligible.

**QuickNode (Bitcoin):** Streams has built-in reorg handling that automatically delivers corrected data. The `requiredConfirmations: 3` on Bitcoin provides adequate depth. If a reorged block notification arrives, the confirmation tracker will see the deposit's block no longer exists at that height and the confirmation count will not advance. If a deposit remains in `confirming` status with stale confirmations for >1 hour, log a warning for manual review — the replacement tx should arrive as a new webhook delivery.

### Observability

Metrics and alerts to add:
- **Ingest QPS** per provider/chain — monitors webhook delivery health
- **Signature failure rate** — security signal
- **Sync failure count** — tracks address sync reliability
- **Reconciliation diffs** — counts mismatches between DB and provider state
- **Confirmation tracker errors per chain** — identifies explorer outages
- **Provider delivery lag** — time between block timestamp and ingest receipt (monitors near-real-time promise)
