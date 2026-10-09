# Controllers and Services Guideline

> Status: HOLDS, guarded by `tests/architecture`: controllers import no
> repository (`TestHTTP_Layer_DoesNotReachPersistence`), look nothing up in the
> container (`TestControllers_Take_TheirDependenciesByConstructor`,
> `TestNo_Service_LocatorOutsideTheCompositionRoot`), and services know nothing of
> `app/http` (`TestServices_Do_NotKnowTheHTTPLayer`). Where a rule below is only
> convention it says so.

A controller does four things, in order: **validate the request, call one
service, map that service's errors to statuses, render a resource.** Anything
else is business logic and belongs in `app/services`, where it is tested with
mocks instead of through HTTP.

```
routes/{admin,api,webhooks,docs}.go   the route tables; build controllers with container.MustMake
  ↓
app/http/middleware              auth, account/wallet scope, permission, throttle
  ↓
app/http/controllers/<surface>/<feature>/   THIN
  ↓
app/services[/<pkg>]             business rules, transactions, sentinels; declares its Store ports
  ↓                ↘
app/repositories   app/adapters  EVERY query / EVERY call to something outside the process
  ↓
app/models                       schema types and the vocabulary everything shares
```

`app/providers` is the composition root: it adapts repositories and adapters to
the ports the services declare, injects `facades.Hash()/Crypt()/Cache()`, and
binds every service by type. Each provider binds the services of its domain
(`WalletServiceProvider`, `WithdrawalServiceProvider`, `DepositServiceProvider`,
…) as a singleton that resolves its own dependencies by type, so a service is
built once and shared; a setter a service still takes runs inside its
singleton, before anyone resolves it. `container.MustMake` is used only in
`bootstrap/`, `routes/`, `app/providers/` and `main.go`. There is no container
struct and no `container.Get()`: `app/container` is only the typed
`Make[T]`/`MustMake[T]` over the Goravel container.

## Dependencies come through the constructor

```go
type WalletsControllerDeps struct {
	Wallets *wallet.Service
}

func NewWalletsController(deps WalletsControllerDeps) *WalletsController {
	return &WalletsController{wallets: deps.Wallets}
}
```

Each controller takes **only the services it uses** through a `Deps` struct of
exported fields filled by name (concrete types, or the narrow interface the
controller declares). No `container.Make` in a controller, no `var svc =
pkg.NewService()` at package level, no `accountSvc()` helper building a service
per call.

## Business logic that was in controllers

Each of these crossed the line in the WaaS. All have moved. Recognise the shape
before writing it again:

| It looked like | It was | Belongs in |
|---|---|---|
| `Register` creating a user, two accounts, two memberships, the default account, then mail, login and a refresh token — no transaction | onboarding | `account.Service.Onboard`: ONE transaction for the rows; mail and session after commit |
| `CreateWalletWithdrawal` (195 lines) verifying TOTP and the passphrase, resolving the amount, estimating the fee, creating/updating an idempotent row and publishing events | a withdrawal | `withdraw.Service.Create` and `Submit` (TOTP and passphrase as ports; idempotency by constraint; the outcome written on the row and published) |
| `ctx.Value("account_environment")` vs `chain.IsTestnet` in five handlers | an environment rule | `chains.Service` (the chain environment filter) |
| `strings.Contains(err.Error(), "unknown chain")`, `err.Error() == "wallet not found"` | a missing sentinel | `chainregistry.ErrUnknownChain` and the wallet and price sentinels, matched with `errors.Is` |
| listing memberships, loading each account, sorting and picking the default at login | an account read model | `account.Service.SignInAccounts` |
| pairing every listed account with the caller's role and failing when one has none | a membership invariant | `account.Service.ListForMemberWithRoles` |
| the create-token handler making the secret, hashing it, building the row and signing the JWT with `middleware.MintAPITokenWithSecret` | issuing a credential | `apitoken.Service.Mint`; the claims and the signing are `apitoken.Claims`/`Sign` (`middleware.MintAPIToken` only signs test fixtures) |
| the add-member handler looking the email up, then adding or inviting, with its 201/202 and four 500 sentences inline | a membership decision | `account.Service.AddMember`, returning `AddedMember` (member or invite) and a sentinel per failed step |
| login, 2FA, refresh, password change and reset, TOTP setup/confirm/disable inline in the auth and user controllers (hashes, token loops, `facades.Crypt()`) | credential flows | `authsvc.SignIn`, `authsvc.Credentials`, `authsvc.TOTPEnrollment`; a session is `authsvc.SessionIssuer`, which takes the request's guard as a `SessionGuard` port |
| `facades.Mail().To(...).Send(...)` in a controller | a notification | a job, see `mail-and-notifications.md` |

A decision (`if status == ...`), a loop over domain objects, time arithmetic, or
two service calls that must agree are all signs. **One service call per
handler**: two that must agree is a transaction, and a transaction belongs to
the service.

Two surfaces serving the same operation with the same behaviour share ONE handler
type in `app/http/controllers` (`SweepHandler`, `AddressesHandler`); each
surface's controller wraps it and keeps its own routes, guards and constructor.
A copy per surface drifts.

A read that fails and is not fatal to the response (a side list on a view) is
logged, never dropped with `_`.

## What crosses the boundary

- **Controllers never import `app/repositories`.** A list filter is a plain input
  struct (or arguments) the service turns into a repository call; a row comes
  back as a model, and the resource shapes it.
- **The service owns its sentinels.** The controller matches
  `sweep.ErrDailyQuotaExceeded`, `withdraw.ErrInsufficientFunds`,
  `chainregistry.ErrUnknownChain` — never `models.ErrRepositoryNotFound`. The
  composition-root adapter maps repository sentinels to the service's own, so
  `errors.Is` holds through the wrapping.
- **A service takes its input as the arguments or the input struct its package
  declares** (`withdraw.CreateInput`, `account.OnboardInput`), filled by straight
  field copy. There is no shared DTO package. **If the copy needs an `if`, the
  `if` belongs in the service.**
- **The caller crosses as the loaded row.** The middleware already read the
  user/token, the account and the wallet; the controller hands
  `requestctx.MustAccount(ctx)` / `MustWallet(ctx)` to the service, which does
  not load them again by id. One read instead of two, and the service acts on the
  row that was authorized.

## Errors a controller can map

```go
func mapError(ctx contractshttp.Context, err error) contractshttp.Response {
	switch {
	case errors.Is(err, wallet.ErrWalletNotFound):
		return responses.Error(ctx, http.StatusNotFound, responses.CodeNotFound, "wallet not found")
	case errors.Is(err, sweep.ErrInsufficientFunds):
		return responses.Fail(ctx, http.StatusUnprocessableEntity, responses.CodeInsufficientFunds, "insufficient_funds")
	default:
		return responses.InternalError(ctx, err)
	}
}
```

The mappers several controllers share are `controllers.MapSweepError`,
`MapWithdrawalError` and `MapSpendingLimitError`. They match sentinels with
`errors.Is` and nothing else: a refusal's sentence is chosen by the service
(`withdraw.CreateRefusal`), and the controller writes it as it is.

If the controller cannot tell a provider failure from a database one, **the
service is missing a sentinel** — add it there. Never `err.Error()` of a wrapped error in a body.

## A service's own shape

- A service declares the **narrow interfaces it needs** and receives them
  through a `Deps` struct of exported fields filled by name (`NewService(Deps)`).
  A ten-argument positional constructor makes a swap the compiler cannot see;
  setters called after construction make a half-built service possible. Neither
  (convention; a few services still take a setter, which runs inside their
  singleton before anyone resolves it).
- **A service never imports `app/http` (guarded), `app/repositories`, `app/adapters`, or
  an I/O library** (go-redis, aws-sdk, net/http clients, go-ethereum's
  `ethclient`). SQS, Secrets Manager, RPC and HTTP providers and the Redis
  set/zset/hash stores are ports implemented in `app/adapters` and injected by the
  provider. Locks, counters and short-lived values use the Cache contract
  (`contractscache.Driver`, injected through `Deps`), not a Redis port; the cache
  driver hides a backend outage (`Add` reports false, `Get` returns the default), so
  fail-closed callers go through `app/services/cacheguard` (`Acquire`, `Count`, `Read`).
  Pure libraries (cryptography, encoding) are allowed and listed in
  `tests/architecture`.
- Framework facades do not appear in a service: the config values, a crypter, the
  cache and a dispatcher come in through `Deps`; a facade that has no port yet is
  reached only through `app/facades`
  (`TestFramework_Facades_ComeThroughAppFacades`).
- Existence is settled by the **constraint, not by a read**.
- A write that is two facts is **one transaction** (`Register`, a withdrawal and
  its ledger rows, a sweep and its child transactions).
- A production type carries **no setter only tests call** and no function field
  that exists only as a test seam (`fetchShareBFn`). The seam is the port.

## Naming a thing once

A magic value is a constant (roles, statuses, chain ids, event names). A second
copy of a helper is a refactor. Early returns over nesting. No abstraction for
one caller.
