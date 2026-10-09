# Service layout

> Status: HOLDS as convention. Only the import directions are guarded
> (`tests/architecture`: a service imports neither `app/http` nor an adapter nor a
> repository). The earlier idea of one flat `package services` with
> `<Subject>Service` types was dropped: the code is one package per subject.

One subject, one package, one `Service`. Do not put several subjects on one struct.

A subject lives in `app/services/<subject>/` (`wallet`, `withdraw`, `account`,
`settings`, `features`, …), package name = folder name:

- `service.go` — the `Service` struct, its `Deps`, `NewService(Deps)` and the
  narrow ports it needs
- other `.go` files by operation or domain (`create.go`, `onboard.go`,
  `spending_limit.go`), each short enough to read at once
- `errors.go` — the package's sentinels, when it has any
- `*_test.go` beside the code — required. A service without tests is unfinished.

A package with several domains keeps its folder and its package name and one file
per domain (`app/services/sweep/`: planner, executor, consolidate, gas readiness,
limits; `app/services/mpc/`: keygen, signing, keystore). A `fakes_test.go` shared by
those domains stays.

Names rely on the package: `wallet.Service`, `wallet.Deps`, `wallet.NewService`.
A type that leaves the package says what it is (`withdraw.CreateInput`,
`withdraw.CreateRefusal`). One-line comment on every exported function.

The service declares a narrow store interface and receives it through `Deps`. It
imports neither `app/http` nor `app/repositories` nor `app/adapters`. Wiring stays
in `app/providers`, and the controller takes the CONCRETE service type (or a
narrow interface of its own) through its constructor.

A check another subject needs is a small exported function plus a narrow
interface, not a dependency on the whole other service.

Adapters are NOT services: a client for an RPC node, a price API, an ingest
provider, SQS or Secrets Manager lives in `app/adapters/<system>/` (see
[`chain-adapters.md`](./chain-adapters.md)).

```go
// ❌ positional constructor: a swap the compiler cannot see
func NewService(registry *chain.Registry, rdb *redis.Client, mpcSvc mpc.Service, sm SecretsManagerAPI, walletRepo repositories.WalletRepository) *Service

// ✅ app/services/wallet/service.go: ports declared here, filled by name
type Deps struct {
	Registry chainLookup
	Wallets  WalletStore
	Secrets  SecretStore
	MPC      mpcKeygen
}
func NewService(deps Deps) *Service
```
