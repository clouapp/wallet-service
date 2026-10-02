# Crypto custody provider design

Date: 2026-09-29.

This spec records decisions already approved. The implementation plan is `docs/superpowers/plans/2026-09-29-essential-custody-gaps.md`.

## Parallel execution

Two tracks start now, together, in the same effort. They run in parallel. Neither track waits on the other.

- **Track Wallets** edits `/home/raphaelcangucu/macro-wallets/back`. It closes the essential-asset gaps for USDT, USDC, BTC, ETH, SOL, and POL: derive an address, detect a deposit, and withdraw.
- **Track Markets** edits `/home/raphaelcangucu/macro-markets/back`. It adds the admin page `settings/crypto-custody`, the feature flags, `WalletProviderFactory`, the `crypto_custody_wallets` rename, the `wallet_addresses.provider` column, and keeps both webhooks live.

Other chains (LTC, XRP, DOGE, DOT, ADA, BCH, AVAX, TON, BNB, TRX, and the rest of that backlog) stay out of this effort.

Execute with Cursor Multitask Mode and subagent-driven development: one subagent per independent task, with a review between tasks. The tracks run at the same time because they touch different repositories. Do not wait for Wallets signing work before starting the Markets factory and settings page. The shared contract both tracks already rely on is in this spec: external API `/api/v1`, the withdrawal `asset` field, and provider values `bitgo` or `macro_wallets`.

## Product

Exactly one provider is active for **new** deposit addresses and **new** withdrawals: `bitgo` or `macro_wallets`. There is no fallback to the other provider. Each provider has its own feature flag, and both flags may be on at once. `CryptoCustodySettings.active_provider` chooses which of the enabled providers is used. New operations fail closed when no provider is selected, or when the selected provider's flag is off. Addresses that already exist keep the provider stored on the row and keep receiving that provider's webhook.

Admin configures both providers on one Filament page, slug `settings/crypto-custody`. The BitGo settings page (`app/Filament/Pages/ManageBitGo.php`, slug `settings/bitGo`) is removed. The page holds the active-provider select and, for the selected provider, that provider's keys, a per-provider `testnet` or `prod` switch, and a connection test. BitGo's test is `GET {express_url}/user/session` with the access token. Macro Wallets' test is `GET {base_url}/api/v1/chains` with `Authorization: Bearer`. `GET /health` does not prove the key.

Provider selection is two Pennant features, `CryptoCustodyBitGoFeature` and `CryptoCustodyMacroWalletsFeature`, plus `active_provider`. Saving the select does not turn the other flag off. `WalletProviderFactory` resolves `active_provider` the same way `app/Factories/PixGatewayFactory.php` resolves a PIX gateway: one `create()` match. An unknown provider, or a selected provider whose flag is off, throws.

`bitgo_wallets` is renamed to `crypto_custody_wallets` and gains a `provider` column (`bitgo` or `macro_wallets`). Existing rows backfill `provider = bitgo`. The same Filament screens stay. Actions that call a BitGo-only API (`consolidate_wallet`, `consolidate_wallet_batch` on `BitgoWalletsResource`) are hidden when `provider = macro_wallets`.

`wallet_addresses.provider` is set at insert and is not editable. Existing rows backfill `bitgo`.

BitGo and Macro Wallets webhooks both stay registered. A credit or a withdrawal completion is applied only when `wallet_addresses.provider` equals the webhook source. A mismatch is acknowledged and not posted to the user ledger.

The user ledger stays in Markets. On-chain funds stay at the custodian that created the address. Macro Wallets does not move funds to BitGo or the reverse.

For `provider = macro_wallets` there is one `crypto_custody_wallets` row per chain. There is no deposit wallet plus withdraw wallet, and no internal transfer between those roles. BitGo rows may keep `type` `deposit` and `withdraw` (`BitgoWallet::DEPOSIT_TYPE`, `BitgoWallet::WITHDRAW_TYPE`). That pair is not created for Macro Wallets.

## Essential assets

In this slice: USDT, USDC, BTC, ETH, SOL, and POL (native Polygon).

USDT and USDC networks in this slice: `eth`, `polygon`, `sol` only. `BSC:USDT`, `BSC:USDC`, and `TRX:USDT` are out. DAI and LINK already have EVM token rows in `database/seeds/tokens.go` and stay out of this slice by product decision. SHIB needs a token row on `eth` and is out. There is no BSC or TRX chain row.

Chains and token rows that already exist and are in scope:

| Ledger code | Chain id | Wallet symbol | Decimals | Contract / mint |
|---|---|---|---|---|
| ETH | `eth` | `eth` | 18 | native. Address, deposit, withdraw, and consolidate already work |
| POL | `polygon` | `POL` | 18 | native |
| BTC | `btc` | `btc` | 8 | native P2WPKH (`addressing.DeriveBtcAddress`) |
| SOL | `sol` | `sol` | 9 | native |
| USDT | `eth` | `USDT` | 6 | `0xdAC17F958D2ee523a2206206994597C13D831ec7` |
| USDC | `eth` | `USDC` | 6 | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |
| USDT | `polygon` | `USDT` | 6 | `0xc2132D05D31c914a87C6611C10748AEb04B58e8F` |
| USDC | `polygon` | `USDC` | 6 | `0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359` |
| USDT | `sol` | `USDT` | 6 | `Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB` |
| USDC | `sol` | `USDC` | 6 | `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v` |

Testnet twins `teth`, `tpolygon`, `tbtc`, `tsol` follow the same rules. `tsol` has a USDC seed (`4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU`, 6 decimals) and no USDT seed. A `tsol` USDT deposit is not credited.

## Official asset constants

New and touched code uses named constants. A file this effort changes does not add a new string literal for an official asset, chain id, token symbol, or provider name. Tests in those files use the constants too. This is not a repo-wide purge of literals in files the plan does not already modify.

Go constants live in the existing block in `app/models/chain.go`. Do not add a second file and do not copy the chain ids into another list. Chain ids already there, including the testnet twins this plan names: `ChainETH` `"eth"`, `ChainPolygon` `"polygon"`, `ChainSOL` `"sol"`, `ChainBTC` `"btc"`, `ChainTETH` `"teth"`, `ChainTPolygon` `"tpolygon"`, `ChainTBTC` `"tbtc"`, `ChainTSOL` `"tsol"`. This effort adds, in that same block:

| Constant | Value |
|---|---|
| `NativeETH` | `eth` |
| `NativePOL` | `POL` |
| `NativeBTC` | `btc` |
| `NativeSOL` | `sol` |
| `SymbolUSDT` | `USDT` |
| `SymbolUSDC` | `USDC` |
| `USDTContractETH` | `0xdAC17F958D2ee523a2206206994597C13D831ec7` |
| `USDCContractETH` | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` |
| `USDTContractPolygon` | `0xc2132D05D31c914a87C6611C10748AEb04B58e8F` |
| `USDCContractPolygon` | `0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359` |
| `USDTMintSOL` | `Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB` |
| `USDCMintSOL` | `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v` |

`NativeETH` and `ChainETH` share the spelling `eth`. They stay separate constants. Polygon native is `NativePOL`. `ChainMatic` already exists and is not an alias for POL. This effort does not map `ChainMatic` to POL and does not delete that old constant. Track Wallets does not compare `bitgo` or `macro_wallets`, so those provider names are not Go constants.

PHP uses `App\Models\Currency`, which already defines `ETH`, `POL`, `BTC`, `SOL`, `USDT`, and `USDC`. Do not add a new class and do not declare the same names on `BitgoWallet`. This effort adds, on `Currency` only:

| Constant | Value |
|---|---|
| `PROVIDER_BITGO` | `bitgo` |
| `PROVIDER_MACRO_WALLETS` | `macro_wallets` |
| `CHAIN_ETH` | `eth` |
| `CHAIN_POLYGON` | `polygon` |
| `CHAIN_BTC` | `btc` |
| `CHAIN_SOL` | `sol` |

Contract and mint addresses stay in the Go block only. JSON payload fixtures and struct-tag examples stay string literals: they are external payloads or compile-time tags. Go and PHP that compare or assign these identifiers use the constants.

## Track Wallets (Macro Wallets)

Track Wallets edits only `/home/raphaelcangucu/macro-wallets/back`. It starts in this effort, in parallel with Track Markets. The other-chain backlog stays out.

Track Wallets finishes a Macro Wallets build that can derive an address, detect a deposit, and withdraw these six assets on `eth`, `polygon`, `sol`, and `btc`:

1. `POST /api/v1/wallets/{walletId}/withdrawals` accepts `asset`. `CreateWalletWithdrawal` today ignores any asset and passes `adapter.NativeAsset()` into `withdraw.WithdrawRequest`, and it converts the human amount with `chain.NativeDecimals` (`wallet_withdrawals_controller.go`). Token withdrawals must convert with the token's decimals. USDT and USDC are 6. An omitted `asset` still means the chain native asset, so current callers keep working. Unknown assets return 422. On `polygon` and `tpolygon` the native asset is `POL`. There is no MATIC alias.

2. SOL and BTC `SignTransaction`, and SOL `BroadcastTransaction`. Both `SignTransaction` methods return an error today. `SolanaLive.BroadcastTransaction` returns an error. `BitcoinLive.BroadcastTransaction` calls JSON-RPC `sendrawtransaction` only; the live client is REST when `RPCURL` contains `blockstream.info` or `mempool.space` (`NewBitcoinLive`), so that path must `POST /tx`. Sweep planning rejects every non-EVM chain (`planner.go` and `consolidate.go` return `ErrUnsupportedChain` unless `AdapterType` is `evm`). BTC and SOL withdraw and consolidate depend on opening that gate and on a real signed transaction. The executor today calls `mpc.Sign`, which rejects `ed25519`. SOL signing uses `ReconstructEd25519PrivateKey` (already on `mpc.Service`) plus `SignTransaction`. BTC signing uses a new `ReconstructSecp256k1PrivateKey` plus `SignTransaction`, because the executor holds TSS shares, not a raw key, and `mpc.Sign` returns a DER signature rather than a raw transaction. EVM withdraw stays on `mpc.Sign` plus `FinalizeMPCSignature`. `EVMLive.SignTransaction` stays the unused stub. SOL and BTC sweeps do not emit an EVM gas-seed transaction. An SPL sweep requires the source to already hold at least 5000 lamports for the fee. A SOL source with tokens and no lamports fails with insufficient native fee. It does not get an EVM-style gas top-up.

3. Solana `GetTokenBalance` derives the associated token account and reads it. The method today returns `SOL token balance not implemented — need ATA derivation`. Without it, USDT and USDC on SOL cannot be planned, consolidated, or withdrawn. A missing token account is a zero balance, not an error.

4. Webhook ingest must persist the seed symbol and the seed decimals for ERC-20 (Alchemy) and SPL (Helius). Confirmed bug: `ingest.Service.processTransfer` sets `asset = transfer.Token.Symbol` whenever `Token != nil`, and neither parser sets `Token.Symbol`. Alchemy keeps the symbol only on `InboundTransfer.Asset` (`USDC` in the payload). Helius sets `Asset` to `spl`. Helius defaults omitted SPL decimals to 9 (`defaultSPLDecimals`), which is wrong for USDT and USDC (6). The EVM block scanner already matches seed contracts in `EVMLive.scanERC20` and sets `Asset` and `Token.Symbol` from the registry. Track Wallets does not change that scanner. A transfer whose contract or mint is not in the registry is skipped and not credited.

## Confirmed and left as-is on Track Wallets

Address derivation for ETH, Polygon, BTC, and SOL already goes through `addressing.DeriveAddress`. `BitcoinLive.DeriveAddress` and `SolanaLive.DeriveAddress` still return errors and are not the live path. Track Wallets does not reimplement them.

BTC block scan sets `DetectedTransfer.To` (`scanBlockRPC` and `scanBlockREST`). SOL block scan does not. `SolanaLive.ScanBlock` diffs `preBalances` and `postBalances` by account index and never reads `accountKeys`, so `To` and `From` are empty. `deposit.Service.processTransfer` looks up `transfer.To`. The scanner therefore cannot credit SOL. SOL deposit detection in this slice is the Helius webhook, which sets `toUserAccount`. Track Wallets does not rewrite `ScanBlock`. SPL deposits are webhook-only in this slice. The scanner does not parse SPL token balances.

`BitcoinLive.GetTransactionBlock` and `SolanaLive.GetTransactionBlock` return `(0, nil)`. Outbound confirmation polling stays EVM-shaped. A Track Wallets withdrawal is complete when the transaction is broadcast and the withdrawal row is stored. It does not add a SOL or BTC confirmation poller.

## Track Markets (Macro Markets)

Track Markets starts now, in parallel with Track Wallets. It edits `/home/raphaelcangucu/macro-markets/back`:

- Feature flags, `WalletProviderFactory`, `settings/crypto-custody`, removal of the BitGo settings page.
- Rename `bitgo_wallets` to `crypto_custody_wallets`, add `provider`, backfill `bitgo`.
- `wallet_addresses.provider`, set on insert, read-only, backfill `bitgo`.
- Both webhooks stay up. Credit only on provider match.
- One Macro Wallets custody wallet per chain. Hide BitGo-only row actions for that provider.

## Out of this effort

Other wallets, with no tasks in either track: LTC, XRP, DOGE, DOT, ADA, BCH, AVAX, TON, BNB, TRX (no chain). SHIB needs a token row on `eth`. DAI and LINK stay out of the essential slice. `BSC:USDT`, `BSC:USDC`, and `TRX:USDT` stay out.

## Done when both tracks land

Track Wallets is done when a Macro Wallets process can derive an address, detect a deposit, and broadcast a withdrawal for ETH, POL, BTC, SOL, and for USDT and USDC on `eth`, `polygon`, and `sol`. Polygon native credits and withdrawals use `POL`. SOL deposit detection is the Helius webhook.

Track Markets is done when the admin page, flags, factory, table rename, address provider column, and dual webhooks are in place, so new addresses and new withdrawals use the selected provider (`bitgo` or `macro_wallets`). Both tracks are this effort.

Both tracks stay open until the Section D gate in `docs/superpowers/plans/2026-09-29-essential-custody-gaps.md` is green. That gate is a real HTTP integration between the Macro Markets process and the Macro Wallets process (auth, `asset`, chain ids, `X-Vault-Signature`, provider values `bitgo` and `macro_wallets`), plus the e2e scenarios for the Macro Wallets admin and user-address path and the separate BitGo backward-compat path. A unit suite that fakes the whole provider does not close either track.
