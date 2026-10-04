# Service layout

> Status: TARGET. Today services are 22 packages of mixed shape. Migration:
> alignment prompt (Part 1) §3.6.

One subject, one service. Do not put several subjects on one struct.

A single subject lives in `app/services/`, package `services`:

- `app/services/<subject>_service.go`
- `app/services/<subject>_service_test.go` — required. A service without it is unfinished.

A package with several domains keeps its folder and its package name. Each
domain inside it is `<domain>_service.go` with one `<domain>_service_test.go`.
In the WaaS that is `app/services/sweep/` (`planner_service.go`,
`executor_service.go`, `consolidate_service.go`, `gas_readiness_service.go`,
`limits_service.go`), `app/services/mpc/` (`keygen_service.go`,
`signing_service.go`, `keystore_service.go`) and `app/services/auth/`. A
`fakes_test.go` shared by those domains stays.

Names carry the subject, because the package is shared: `WalletsService`,
`NewWalletsService`, `WalletsDeps`, `WalletStore`. Prefix a generic name
(`Service`, `New`, `Deps`). Before a type joins package `services`, its name must
not already exist there. One-line comment on every function.

The service declares a narrow store interface and receives it through its deps.
It imports neither `app/http` nor `app/repositories` nor `app/adapters`. Wiring
stays in `app/providers`, and the controller takes the CONCRETE service type
through its constructor.

A check another subject needs is a small exported function plus a narrow
interface (`RequireActiveWallet(ctx, WalletLookup, id)`), not a dependency on the
whole other service.

Adapters are NOT services: a client for an RPC node, a price API, an ingest
provider, SQS or Secrets Manager lives in `app/adapters/<system>/` (see
[`chain-adapters.md`](./chain-adapters.md)).

```go
// ❌ app/services/wallet/service.go
package wallet
func NewService(registry *chain.Registry, rdb *redis.Client, mpcSvc mpc.Service, sm SecretsManagerAPI, walletRepo repositories.WalletRepository, addressRepo repositories.AddressRepository) *Service

// ✅ app/services/wallets_service.go
package services
func NewWalletsService(d WalletsDeps) *WalletsService
```
