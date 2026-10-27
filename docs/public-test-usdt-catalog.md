# Existing public USDT test assets

This catalog change is source preparation only. It does not request faucet
tokens, mint, deploy a token, transfer funds, enroll a live token, or deploy the
service. Operator review of third-party test asset identity is required before
live catalog alignment. These assets are not represented as Tether-issued USDT.

`USDT` is the custody API's logical asset key. The contract or mint, physical
network and six-decimal precision define the actual test asset. It is not safe to
identify an asset from its symbol alone. The token name retains the provider/test
identity, including the `USDTMock` or `devUSDT` alias where applicable.

| Canonical record | Physical test network | Existing contract or mint | Provider-documented symbol | Catalog label |
| --- | --- | --- | --- | --- |
| `polygon` | Polygon Amoy, chain ID `80002` | `0x164F5A056166d8F2ce09FdAc6d040209a8C94d01` | `USDTMock` | Public test USDT (Zama; on-chain USDTMock) |
| `sol` | Solana Devnet | `H8UekPGwePSmQ3ttuYGPU1szyFfjZR4N53rymSFwpLPm` | `devUSDT` | devUSDT (Orca public test token) |
| `arbitrum` | Arbitrum Sepolia, chain ID `421614` | `0xE30928528f52CAEeB75fB07837e22d77D47e9c07` | `USDT` | Public test USDT (Push) |

All three use six decimals. The Solana SPL mint account stores precision and
authority information, not a symbol: `devUSDT` is the Orca tutorial's documented
name, not a symbol independently read from the mint account. Published provider
documentation does not establish that Orca or Push is the issuer of the asset.

## Primary provenance and distribution boundaries

- [Zama testnet registry](https://github.com/zama-ai/protocol-registry/blob/main/testnet.md)
  identifies the exact Amoy deployment as `AMOY_USDT_MOCK`.
  Its [verified contract source](https://amoy.polygonscan.com/address/0x164F5A056166d8F2ce09FdAc6d040209a8C94d01#code)
  labels it a mock, uses six decimals, and limits the existing public distribution
  function to one million tokens per call. The approval implementation requires
  resetting a nonzero allowance to zero before another nonzero approval.
- [Orca's existing devtoken tutorial](https://github.com/orca-so/whirlpools-sdk-tutorial-legacy#devtokens)
  publishes the exact devUSDT mint and six-decimal precision. Its
  [existing conversion example](https://github.com/orca-so/whirlpools-sdk-tutorial-legacy/blob/main/src/EN/convert_sol_to_dev_token.ts)
  describes a devSOL-to-devUSDT route. This patch does not invoke that route or
  assume an unrestricted mint authority.
- [Push's test gateway address book](https://push.org/docs/chain/setup/smart-contract-address-book/)
  publishes the exact Arbitrum Sepolia source token.
  [Push's faucet documentation](https://push.org/docs/chain/setup/faucets/)
  describes its existing public distribution function. Token-specific limits,
  cooldown, current proxy implementation and availability require operator
  verification before requesting tokens; generic example amounts are not a
  guarantee for this contract.

The [Tether WDK testnet guidance](https://docs.wdk.tether.io/sdk/wallet-modules/wallet-evm-erc-4337/configuration/)
explicitly distinguishes third-party Sepolia test tokens from Tether Tokens.
Support for a network in an SDK does not prove issuer provenance for a token.

## Seeding and alignment

The overlay is restricted to these three physical test networks and canonical
records. Mainnet lists, native coins, Circle USDC, the existing Sepolia BRLA mock,
and legacy `tpolygon`, `tsol` and `tarbitrum` seed lists remain unchanged.

For an otherwise aligned existing testnet catalog, `chains:align-tokens` previews
exactly three creations. Preview writes nothing. A separately reviewed alignment
uses the existing repository path and shared active-token catalog, so it is not
limited to one tenant. No new issuer, contract, wallet or mint is created.
The service and workers must reload after an approved alignment as described by
the existing command. This patch does not execute an alignment.

Unknown custom contracts and their metadata remain untouched. A contract already
registered by an operator is not silently renamed to the logical alias. Mainnet
rows remain unchanged even if an operator uses the same address string there.
An active custom `USDT` contract on the same canonical test chain refuses the
entire new-asset alignment with an explicit conflict error. No operator token is
retired or replaced to make room. Known foreign-network or invalid legacy rows
are exempt only when the alignment explicitly plans their retirement. Seeding
does not assume those retirements and refuses a collision until it is resolved.
Apply rechecks all three candidates under physical-chain locks in one transaction;
the requested test profile cannot override the actual adapter, testnet flag or
network ID. This locking protocol does not serialize an unrelated writer that
ignores it.
The invalid historical Amoy `0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13`
stays inactive and is never reused as a fallback.
