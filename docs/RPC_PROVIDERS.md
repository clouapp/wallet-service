# Blockchain RPC Provider Guide

This guide explains where to obtain production-grade RPC endpoints for the multi-chain wallet service.

## Overview

The wallet service requires reliable RPC endpoints for:
- **Ethereum** (and EVM-compatible chains)
- **Polygon**
- **Solana**
- **Bitcoin**

For local development, you can use the free public endpoints provided in `.env.dev`. However, **production deployments require dedicated RPC providers** with higher rate limits, better uptime guarantees, and SLA support.

---

## Recommended RPC Providers

### 1. Alchemy (Recommended for ETH & Polygon)

**Best for**: Ethereum, Polygon, and other EVM chains
**Website**: https://www.alchemy.com

#### Features:
- Free tier: 300M compute units/month
- Enhanced APIs (NFT API, Token API, Notify API)
- WebSocket support
- Archive node access
- 99.9% uptime SLA

#### Supported Networks:
- Ethereum Mainnet & Sepolia Testnet
- Polygon Mainnet & Amoy Testnet
- Arbitrum, Optimism, Base, etc.

#### How to Get Started:
1. Sign up at https://dashboard.alchemy.com/signup
2. Create a new app
3. Select your network (e.g., "Ethereum" → "Mainnet")
4. Copy your HTTP endpoint: `https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY`
5. Add to `.env.prod`:
   ```bash
   ETH_RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY
   POLYGON_RPC_URL=https://polygon-mainnet.g.alchemy.com/v2/YOUR_API_KEY
   ```

#### Pricing:
- **Free**: 300M compute units/month
- **Growth**: $49/month (up to 4B compute units)
- **Scale**: Custom pricing for enterprise

---

### 2. Infura (Alternative for ETH & Polygon)

**Best for**: Ethereum, Polygon, and IPFS
**Website**: https://www.infura.io

#### Features:
- Free tier: 100K requests/day
- WebSocket support
- IPFS gateway included
- Multi-region infrastructure

#### Supported Networks:
- Ethereum Mainnet, Sepolia, Holesky
- Polygon Mainnet
- Arbitrum, Optimism, Avalanche

#### How to Get Started:
1. Sign up at https://app.infura.io/register
2. Create a new project
3. Copy your project ID from the dashboard
4. Use endpoint: `https://mainnet.infura.io/v3/YOUR_PROJECT_ID`
5. Add to `.env.prod`:
   ```bash
   ETH_RPC_URL=https://mainnet.infura.io/v3/YOUR_PROJECT_ID
   POLYGON_RPC_URL=https://polygon-mainnet.infura.io/v3/YOUR_PROJECT_ID
   ```

#### Pricing:
- **Free**: 100K requests/day
- **Developer**: $50/month (1M requests/day)
- **Team**: $225/month (10M requests/day)

---

### 3. QuickNode (Multi-Chain Support)

**Best for**: All chains (ETH, Polygon, Solana, Bitcoin)
**Website**: https://www.quicknode.com

#### Features:
- Support for 20+ blockchains including Solana and Bitcoin
- Free tier: 10M API credits/month
- Auto-scaling infrastructure
- Add-ons: Archive data, trace API, debug API

#### Supported Networks:
- Ethereum, Polygon
- **Solana Mainnet & Devnet**
- **Bitcoin Mainnet & Testnet**
- BSC, Arbitrum, Optimism, Fantom, etc.

#### How to Get Started:
1. Sign up at https://dashboard.quicknode.com/signup
2. Click "Create an endpoint"
3. Select chain (Ethereum, Polygon, Solana, or Bitcoin)
4. Select network (Mainnet/Testnet)
5. Copy your HTTP endpoint
6. Add to `.env.prod`:
   ```bash
   ETH_RPC_URL=https://your-endpoint-name.quiknode.pro/YOUR_API_KEY/
   SOLANA_RPC_URL=https://your-solana-endpoint.quiknode.pro/YOUR_API_KEY/
   BTC_RPC_URL=https://your-btc-endpoint.quiknode.pro/YOUR_API_KEY/
   ```

#### Pricing:
- **Free**: 10M API credits/month
- **Build**: $49/month (100M credits)
- **Scale**: $299/month (1B credits)

---

### 4. Helius (Best for Solana)

**Best for**: Solana only
**Website**: https://www.helius.dev

#### Features:
- Free tier: 100K credits/day
- Enhanced Solana APIs (webhooks, parsed transactions, DAS API)
- Dedicated RPC nodes
- Priority fee API

#### Supported Networks:
- Solana Mainnet-Beta
- Solana Devnet

#### How to Get Started:
1. Sign up at https://dashboard.helius.dev/signup
2. Create a new project
3. Copy your API key
4. Use endpoint: `https://mainnet.helius-rpc.com/?api-key=YOUR_API_KEY`
5. Add to `.env.prod`:
   ```bash
   SOLANA_RPC_URL=https://mainnet.helius-rpc.com/?api-key=YOUR_API_KEY
   ```

#### Pricing:
- **Free**: 100K credits/day
- **Developer**: $29/month (1M credits/day)
- **Professional**: $99/month (5M credits/day)

---

### 5. Blockstream (Bitcoin)

**Best for**: Bitcoin only
**Website**: https://blockstream.com

#### Features:
- Free public API (rate-limited)
- Esplora REST API
- No authentication required for basic usage

#### Supported Networks:
- Bitcoin Mainnet
- Bitcoin Testnet

#### How to Get Started:
1. No signup required for basic usage
2. Use public endpoints:
   ```bash
   # Mainnet
   BTC_RPC_URL=https://blockstream.info/api

   # Testnet
   BTC_RPC_URL=https://blockstream.info/testnet/api
   ```

#### Limitations:
- Rate limits apply (not specified publicly)
- For production, consider running your own Bitcoin node or using QuickNode

---

## Alternative: Self-Hosted Nodes

For maximum control and no rate limits, you can run your own blockchain nodes.

### Ethereum/Polygon Node:
- **Geth** (Ethereum): https://geth.ethereum.org
- **Erigon** (Archive node): https://github.com/ledgerwatch/erigon
- **Bor** (Polygon): https://github.com/maticnetwork/bor

### Solana Validator:
- https://docs.solana.com/running-validator

### Bitcoin Core:
- https://bitcoin.org/en/full-node

**Note**: Self-hosting requires significant infrastructure (1TB+ storage for archive nodes, high bandwidth, DevOps expertise).

---

## Configuration Examples

### Development (.env.dev)
```bash
# Free public endpoints - OK for testing
ETH_RPC_URL=https://eth-sepolia.public.blastapi.io
POLYGON_RPC_URL=https://rpc-amoy.polygon.technology
SOLANA_RPC_URL=https://api.devnet.solana.com
BTC_RPC_URL=https://blockstream.info/testnet/api
```

### Production (.env.prod)
```bash
# Dedicated provider endpoints - Required for production
ETH_RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_ALCHEMY_KEY
POLYGON_RPC_URL=https://polygon-mainnet.g.alchemy.com/v2/YOUR_ALCHEMY_KEY
SOLANA_RPC_URL=https://mainnet.helius-rpc.com/?api-key=YOUR_HELIUS_KEY
BTC_RPC_URL=https://your-btc-endpoint.quiknode.pro/YOUR_API_KEY/
```

---

## Fallback Endpoints and Failover

Each chain has an ordered endpoint list. Position 1 is the primary (`chains.rpc_url`, what
`chains:set-rpc` writes); positions 2 and after are fallbacks (`chain_rpc_endpoints`), tried in order
when the endpoints before them cannot serve a call. URLs are sealed with `APP_KEY` at rest and are
never printed: commands and the platform API show an endpoint by its host and a fingerprint.
EVM chains (`eth`, `polygon`, `bsc`, `base`, `arbitrum` and their `t`-prefixed records) and Solana
(`sol`, `tsol`) fail over through this list. Bitcoin-family chains use `<PREFIX>_FALLBACK_RPC_URL`;
TRON and XRP have no fallback yet.

### Read the list and its health

```bash
go run . artisan chains:rpc-endpoints eth           # position, host, enabled, recorded health
go run . artisan chains:rpc-endpoints eth --probe   # also asks every endpoint now: network, head, latency
```

```
chain eth (evm, ethereum-sepolia): 3 endpoint(s)
1 primary  enabled  sepolia.drpc.org  health: healthy  probe: ethereum-sepolia, head 11947549, 140ms
2 fallback enabled  sepolia.gateway.tenderly.co  health: skipped until 2026-10-19T12:04:00Z, 3 failure(s) in a row, last error timeout at 2026-10-19T12:03:30Z
3 fallback disabled ethereum-sepolia-rpc.publicnode.com  health: not recorded
health recorded by the deposit_scanner process at 2026-10-19T12:03:30Z
```

- `health` is what the last process that saw a change recorded (Redis `vault:rpc-health:<chain>`, kept
  24 h): `healthy`, `failing` (N failures in a row), `skipped until <time>` (in a cool-down: 3 failures
  in a row skip an endpoint for 30 s, doubling up to 5 min; one more failure after a cool-down skips it
  again, a success clears it), or `serves another network, not used`. `not recorded` means no process
  saw that endpoint change state in the last day (it answered every call, or was never called).
- `last error` is a coarse class (`timeout`, `connection refused`, `http 503`, `rate limited`,
  `json-rpc error -32603`, ...), never the provider's text.
- The logs of the API, the workers and the Lambdas say when a chain leaves its primary and when it comes
  back (`rpc switched away from the primary endpoint`, `rpc back on the primary endpoint`, with the chain
  and hosts), and when an endpoint is skipped for a cool-down.
- Platform API: `GET /v1/platform/chains/{chainId}/rpc-endpoints[?probe=true]`.

### Add, reorder, enable, disable, remove

Every command is a dry run until `--apply`, and takes one change at a time:

```bash
go run . artisan chains:rpc-endpoints eth --add=https://sepolia.drpc.org            # at the end
go run . artisan chains:rpc-endpoints eth --add=env:ETH_FALLBACK_RPC_URL --position=2 # a URL with a key, kept in the env
go run . artisan chains:rpc-endpoints eth --order=2,1,3 --apply                       # position 2 becomes the primary
go run . artisan chains:rpc-endpoints eth --disable=3 --apply                         # kept in the list, not called
go run . artisan chains:rpc-endpoints eth --enable=3 --apply
go run . artisan chains:rpc-endpoints eth --remove=3 --apply
go run . artisan chains:rpc-endpoints eth --set=https://a.example,https://b.example --apply  # the whole list, primary first
go run . artisan chains:rpc-endpoints eth --defaults --apply                          # the network's checked defaults
```

- Before anything is written, every endpoint the change adds or enables, and a new primary, is asked
  which network it serves: `eth_chainId` must be the chain's `network_id`, a Solana `getGenesisHash`
  must be its cluster's. An endpoint on another network, or one that does not answer (a filtered port,
  a typo), refuses the change. `chains:set-rpc` checks a new primary the same way.
- Position 1 (the primary) is always used: to stop using it, move another endpoint there with `--order`
  (or `--set`), then disable or remove the old one. A disabled endpoint cannot become the primary.
- An `env:NAME` entry must name a variable set in the shell that runs the command (it is resolved to
  check the network) and in the environment of every process.
- **Restart the API, the workers and the Lambdas after a change**: the chain adapters read the list at
  boot. A change through the platform API is used by the API process at once.
- Defaults (`app/services/rpcendpoints/defaults.go`): Sepolia only, in this order:
  `https://sepolia.drpc.org`, `https://sepolia.gateway.tenderly.co`,
  `https://ethereum-sepolia-rpc.publicnode.com` (load-balanced, answered `null` for a mined receipt
  6 of 6 times on 2026-10-18, so it comes last). `1rpc.io/sepolia` is not listed: it rate-limits
  `eth_getLogs` and refuses `eth_getBlockReceipts`.
- Platform API (platform admins): `POST .../rpc-endpoints` `{"url","position"}`, `PUT .../rpc-endpoints`
  `{"urls":[...]}`, `POST .../rpc-endpoints/order` `{"order":[2,1,3]}`, `POST .../rpc-endpoints/defaults`,
  `PATCH .../rpc-endpoints/{position}` `{"enabled":false}`, `DELETE .../rpc-endpoints/{position}`.

### What moves a call to the next endpoint

| Moves on | Answered at once |
|---|---|
| transport error, timeout, unreadable body, HTTP 5xx/408 | invalid params, execution reverted |
| HTTP 429/403, JSON-RPC 429/-32005, a rate-limit message | nonce too low, insufficient funds, underpriced |
| JSON-RPC -32603, -32014, -32016, "header not found", "missing trie node", "unknown block" | a skipped Solana slot, a transaction version not supported |
| method not found / not supported / not allowed (-32601, -32004; not counted as a failure) | any other JSON-RPC error |

- A `null` receipt, transaction or block (`eth_getTransactionReceipt`, `eth_getTransactionByHash`,
  `eth_getBlockByNumber`, `eth_getBlockByHash`, Solana `getTransaction`, `getBlock`) asks the next
  endpoint before it is believed: a load-balanced provider can answer from a node that does not have it.
- A broadcast moves on only when the previous endpoint certainly did not take the transaction (the
  connection was never made, it rate-limited the call, or it does not serve the method); after a
  timeout or a 5xx it stops and the withdrawal reconciles as before. "already known" from a later
  endpoint is a success.
- An EVM block and its logs are read from the same endpoint.

## Rate Limits & Best Practices

### Public Endpoints (Free)
- **Rate limits**: 10-50 requests/second (varies by provider)
- **Use for**: Local development, testing only
- **Not recommended for**: Production deployments

### Dedicated Providers
- **Rate limits**: 100-1000+ requests/second (depends on tier)
- **Use for**: Staging and production environments
- **Best practices**:
  - Implement request caching (Redis)
  - Use WebSockets for real-time data
  - Monitor API usage via provider dashboards
  - Set up alerts for quota limits

### Redundancy Strategy
Give every EVM and Solana chain **fallback endpoints from other providers**: the service tries them in
order when the primary cannot serve a call. See [Fallback Endpoints and Failover](#fallback-endpoints-and-failover)
below. Bitcoin-family chains take their fallbacks from `<PREFIX>_FALLBACK_RPC_URL` (`CLAUDE.md`,
"Bitcoin-family provider failover").

---

## Cost Estimation

For a production wallet service handling **10M requests/month**:

| Chain    | Provider | Plan       | Cost/Month |
|----------|----------|------------|------------|
| Ethereum | Alchemy  | Growth     | $49        |
| Polygon  | Alchemy  | Growth     | $49        |
| Solana   | Helius   | Developer  | $29        |
| Bitcoin  | QuickNode| Build      | $49        |
| **Total**|          |            | **$176**   |

**Savings Tip**: Use QuickNode for all chains if possible (single provider = simpler billing).

---

## Support & Documentation

- **Alchemy Docs**: https://docs.alchemy.com
- **Infura Docs**: https://docs.infura.io
- **QuickNode Docs**: https://www.quicknode.com/docs
- **Helius Docs**: https://docs.helius.dev
- **Bitcoin RPC**: https://developer.bitcoin.org/reference/rpc/

---

## Quick Start Checklist

- [ ] Sign up for Alchemy (ETH + Polygon)
- [ ] Sign up for Helius (Solana)
- [ ] Sign up for QuickNode or use Blockstream (Bitcoin)
- [ ] Copy API keys to `.env.prod`
- [ ] Test connections with `make test-rpc` (if implemented)
- [ ] Set up billing alerts in provider dashboards
- [ ] Configure backup RPC endpoints for redundancy

---

## Webhook Providers (Deposit Monitoring)

Incoming **deposit detection** is driven by **provider webhooks** (push notifications to your API), not by high-frequency RPC block polling. You still use the RPC URLs above for **wallet balance checks**, **transaction broadcast**, and other on-demand reads—so overall RPC volume drops sharply compared to a poll-every-block scanner.

### Providers by chain

| Role | Provider | Chains | Notes |
|------|----------|--------|--------|
| EVM deposits | [Alchemy](https://www.alchemy.com) | Ethereum, Polygon (mainnet + testnets) | Notify / Address Activity webhooks |
| Solana deposits | [Helius](https://www.helius.dev) | Solana mainnet & devnet | Enhanced webhooks (e.g. enhanced / enhancedDevnet) |
| Bitcoin deposits | [QuickNode](https://www.quicknode.com) | Bitcoin mainnet & testnet | Streams (webhook delivery to your HTTPS URL) |

### Webhook documentation

- **Alchemy Notify**: [Address Activity & Notify API](https://docs.alchemy.com/reference/notify-api-quickstart)
- **Helius**: [Webhooks](https://docs.helius.dev/webhooks-and-websockets/webhooks)
- **QuickNode Streams**: [Streams overview](https://www.quicknode.com/docs/streams)

Subscriptions are created through each provider’s dashboard or API; they are **not** inserted by database seeds. Point webhook destinations at your public **ingest** base URL (see `.env.example`: `WEBHOOK_INGEST_BASE_URL`).

### RPC usage after webhooks

- **Deposits**: primary signal from webhooks; reconcilers may use provider APIs for drift checks.
- **RPC**: balances, fee estimation, `eth_getTransactionReceipt`-style flows where needed, and **broadcasting signed withdrawals**.

### Cost estimation (webhook-first deposits)

With deposit monitoring off the hot RPC path, you can often stay on **lower RPC tiers** than a 10M+/month polling workload. Ballpark for a mid-size custody stack (same four chains), assuming webhooks cover deposit detection and RPC is moderate:

| Component | Typical approach | Indicative monthly cost |
|-----------|------------------|-------------------------|
| EVM RPC (Alchemy) | Free or Growth for reads + broadcast | $0–$49 |
| Polygon RPC (Alchemy) | Same app / key as ETH where possible | $0–$49 |
| Solana RPC (Helius) | Developer or lower if not polling blocks | ~$0–$29 |
| Bitcoin RPC (QuickNode) | Light REST for balance / broadcast | $0–$49 |
| Webhook / Streams | Provider-specific (often included or low fixed add-on) | Varies |

**Previous** “10M RPC requests/month” style estimates assumed aggressive polling; webhook-first flows usually **reduce** RPC spend; add any **paid Streams / webhook** SKUs from QuickNode or Alchemy if your plan charges for them.

---

**Last Updated**: March 2026
**Maintained By**: Vault Custody Service Team
