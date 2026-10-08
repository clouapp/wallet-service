# Controllers and Services Guideline

> Status: TARGET. Today ~100 of the 147 dependency lookups in controllers are
> repositories, and several handlers carry whole business flows. Migration:
> alignment prompt (Part 1) §3.3, §3.6.

A controller does four things, in order: **validate the request, call one
service, map that service's errors to statuses, render a resource.** Anything
else is business logic and belongs in `app/services`, where it is tested with
mocks instead of through HTTP.

```
routes/<surface>_<feature>.go   the route table; resolves services from app/container
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
binds every service by type. `tests/architecture` enforces the direction, and
it is blocking.

## Dependencies come through the constructor

```go
type WithdrawalsController struct {
	withdrawals *services.WithdrawalsService
}

func NewWithdrawalsController(withdrawals *services.WithdrawalsService) *WithdrawalsController {
	return &WithdrawalsController{withdrawals: withdrawals}
}
```

Each controller takes **only the services it uses**, concretely. No
`container.Get().X`, no global container struct, no `var svc = pkg.NewService()`
at package level, no `accountSvc()` helper building a service per call.

## Business logic that was in controllers

Each of these crossed the line in the WaaS and is being moved. Recognise the
shape before writing it again:

| It looked like | It was | Belongs in |
|---|---|---|
| `Register` creating a user, two accounts, two memberships, the default account, then mail, login and a refresh token — no transaction | onboarding | `AuthService.Register`: ONE transaction for the rows; mail and session after commit |
| `CreateWalletWithdrawal` (195 lines) verifying TOTP and the passphrase, resolving the amount, estimating the fee, creating/updating an idempotent row and publishing events | a withdrawal | `WithdrawalsService.Create` (TOTP and passphrase as ports; idempotency by constraint) |
| `ctx.Value("account_environment")` vs `chain.IsTestnet` in five handlers | an environment rule | the scope middleware / `app/policies` (see `authorization.md`) |
| `strings.Contains(err.Error(), "unknown chain")` | a missing sentinel | `chainregistry.ErrUnknownChain`, matched with `errors.Is` |
| `MintAPIToken` living in middleware and called from a controller | issuing a credential | `ApiTokensService` |
| `facades.Mail().To(...).Send(...)` in a controller | a notification | a job, see `mail-and-notifications.md` |

A decision (`if status == ...`), a loop over domain objects, time arithmetic, or
two service calls that must agree are all signs. **One service call per
handler**: two that must agree is a transaction, and a transaction belongs to
the service.

## What crosses the boundary

- **Controllers never import `app/repositories`.** A list filter is a DTO the
  service turns into a repository call; a row comes back as a model.
- **The service owns its sentinels.** The controller matches
  `services.ErrWalletNotFound`, `sweep.ErrSweepLimitExceeded`,
  `withdraw.ErrInsufficientFunds` — never `models.ErrRepositoryNotFound`. The
  composition-root adapter maps repository sentinels to the service's own, so
  `errors.Is` holds through the wrapping.
- **A service takes its input as a DTO** from `app/dtos/<feature>_dto.go`, filled
  by straight field copy. **If the copy needs an `if`, the `if` belongs in the
  service.**
- **The caller crosses as the loaded row.** The middleware already read the
  user/token, the account and the wallet; the controller hands
  `middleware.CurrentAccount(ctx)` / `CurrentWallet(ctx)` to the service, which
  does not load them again by id. One read instead of two, and the service acts
  on the row that was authorized.

## Errors a controller can map

```go
func mapError(ctx contractshttp.Context, err error, action string) contractshttp.Response {
	switch {
	case errors.Is(err, services.ErrWalletNotFound):
		return responses.Error(ctx, http.StatusNotFound, resources.CodeNotFound, "wallet not found")
	case errors.Is(err, withdraw.ErrInsufficientFunds):
		return responses.Error(ctx, http.StatusUnprocessableEntity, resources.CodeInsufficientFunds, "insufficient funds")
	case errors.Is(err, chain.ErrProviderUnavailable):
		return responses.ProviderError(ctx, err, "the network is unavailable, try again")
	default:
		return responses.InternalError(ctx, fmt.Errorf("%s: %w", action, err))
	}
}
```

If the controller cannot tell a provider failure from a database one, **the
service is missing a sentinel** — add it there. Never `err.Error()` in a body.

## A service's own shape

- A service declares the **narrow interfaces it needs** and receives them
  through a `Deps` struct of exported fields filled by name. A ten-argument
  positional constructor (`sweep.NewService(...)`) makes a swap the compiler
  cannot see; setters called after construction (`SetDepositEvents`) make a
  half-built service possible. Neither.
- **A service never imports `app/http`, `app/repositories`, `app/adapters`, or
  an I/O library** (go-redis, aws-sdk, net/http clients, go-ethereum's
  `ethclient`). SQS, Secrets Manager, RPC and HTTP providers and the Redis
  set/zset/hash stores are ports implemented in `app/adapters` and injected by the
  provider. Locks, counters and short-lived values use the Cache contract
  (`contractscache.Driver`, injected through `Deps`), not a Redis port; the cache
  driver hides a backend outage (`Add` reports false, `Get` returns the default), so
  fail-closed callers go through `app/services/cacheguard` (`Acquire`, `Count`, `Read`).
  Pure libraries (cryptography, encoding) are allowed and listed in
  `tests/architecture`.
- `facades.Event()`, `facades.Config()`, `facades.Crypt()` do not appear in a
  service: a dispatcher, the config values and a crypter come in through `Deps`.
- Existence is settled by the **constraint, not by a read**.
- A write that is two facts is **one transaction** (`Register`, a withdrawal and
  its ledger rows, a sweep and its child transactions).
- A production type carries **no setter only tests call** and no function field
  that exists only as a test seam (`fetchShareBFn`). The seam is the port.

## Naming a thing once

A magic value is a constant (roles, statuses, chain ids, event names). A second
copy of a helper is a refactor. Early returns over nesting. No abstraction for
one caller.
