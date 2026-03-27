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
For production, consider using **multiple providers** with fallback logic:

```go
primaryRPC := os.Getenv("ETH_RPC_URL")        // Alchemy
fallbackRPC := os.Getenv("ETH_RPC_URL_BACKUP") // Infura

// Try primary, fallback to backup on failure
```

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
