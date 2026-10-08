# HTTP Layer Guideline

> Status: TARGET. Today handlers are free functions in one `controllers` package,
> write JSON inline and read the caller from `ctx.Value("string")`. Migration:
> alignment prompt (Part 1) §3.2–§3.5.

The HTTP layer binds a request, calls **one** service, maps that service's
errors and renders a resource. Everything it needs lives in four packages under
`app/http/`. A controller package never redefines them.

| Package | Use it for |
|---|---|
| `requests` | running a form request (`Validate`, `ValidatePath`), a path id (`RouteID`, `ParseUUID`), a list window (`Paging`) |
| `responses` | every failure body: `Error`, `InternalError`, `ProviderError` (502 for a chain/RPC/provider failure: logs the cause, answers the endpoint's own message), `ValidationFailed`, `FieldError` |
| `resources` | the wire shape. **JSON tags exist only here** — a model carries none, not even `json:"-"` |
| `middleware` | who is asking and what they may reach: `CurrentUser`, `CurrentAPIToken`, `CurrentAccount`, `CurrentWallet`, `HasPermission`, `Throttle` |

## Two surfaces, never mixed

| Surface | Prefix | Caller | Controllers |
|---|---|---|---|
| dashboard | `/v1` | a `User` with a session (Goravel JWT guard), acting in an account | `app/http/controllers/dashboard/<feature>/` |
| external | `/api/v1` | an `ApiToken` bound to ONE account (JWT + optional `X-Signature` HMAC) | `app/http/controllers/external/<feature>/` |

Inbound provider webhooks (`/v1/webhooks/ingest/{provider}/{chainID}`) are a
third, unauthenticated-by-session route group whose authenticity is the
provider's signature; they live in `controllers/webhooks/`.

**A handler serves one surface.** When both surfaces expose the same operation
(list wallets, generate address, create withdrawal…), each surface has its own
thin handler calling the same service method. A handler that asks
`ctx.Value("user_id")` to find out which caller it has is two handlers glued
together, and the `if` decides security (e.g. whether TOTP is required).

## The shape of a handler

```go
type WalletsController struct {
	wallets *services.WalletsService
}

func NewWalletsController(wallets *services.WalletsService) *WalletsController {
	return &WalletsController{wallets: wallets}
}

func (c *WalletsController) CreateWallet(ctx contractshttp.Context) contractshttp.Response {
	account := middleware.CurrentAccount(ctx)

	var req walletsrequests.CreateWalletRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	wallet, err := c.wallets.CreateWallet(ctx.Context(), account, dtos.CreateWalletDTO{
		Chain:      req.Chain,
		Label:      req.Label,
		Passphrase: req.Passphrase,
	})
	if err != nil {
		return mapError(ctx, err, "creating wallet")
	}
	return ctx.Response().Status(http.StatusCreated).Json(walletsresources.WalletFrom(wallet))
}
```

Four steps, in that order, and nothing else. Dependencies come through the
**constructor**, with the concrete service — no `container.Get()`, no
`facades.App()`, no repository, no package-level `var authService = …`. The
route table builds the controller with `container.MustMake[*services.WalletsService]()`,
so a missing binding fails at boot, not on the first request. Middleware and
resources follow the same rule: a middleware factory takes what it reads
(`middleware.Can(accounts, perm)`, `middleware.WalletContext(deps)`) and a
resource only shapes data its controller already loaded. `container.MustMake`
stays in `bootstrap/`, `routes/`, `app/providers/` and `main.go`.

## Who is asking

See [`identity-and-scope.md`](./identity-and-scope.md). In short: the auth
middleware puts the authenticated `User` or `ApiToken` on the request context
under an **unexported** key, the scope middleware adds the `Account` and (on
wallet routes) the `Wallet`, and handlers read them only through
`middleware.CurrentUser / CurrentAPIToken / CurrentAccount / CurrentWallet`.
A handler behind the guard never nil-checks them and never writes that 401/404
itself. There is no `ctx.Value("account_id")` anywhere outside `app/http/middleware`.

**The account and the wallet come from the path or the authenticated token,
never from the body.** On the dashboard the account comes from the
`X-Account-Id` header or `{accountId}` and is checked against the user's
membership; on the external API it is the token's account and nothing else.
An account or wallet id read off a payload turns every route into a
cross-account read.

## Validating a request

```go
var req withdrawalsrequests.CreateWithdrawalRequest
if response := requests.Validate(ctx, &req); response != nil {
	return response
}
```

`Validate` answers the failure itself, so a handler only continues or returns.
The status and code of each outcome are in
[`http-error-contract.md`](./http-error-contract.md).

Rules:

- **Body input has one entry point: `requests.Validate`** (it wraps
  `ctx.Request().ValidateRequest`). A controller never calls `ValidateRequest`,
  `Bind`, `Input`, `All` or `Json` itself, and there is no per-package wrapper;
  `TestHandlers_Read_TheirInputThroughAFormRequest` refuses them.
- **A path or query parameter is read in the handler** with
  `ctx.Request().Route("id")` / `Query("limit")`, the documented Goravel way
  (D2). A form request exists only where there are rules to run: no
  `Load`-only request that copies a parameter into a struct.
- Form requests live in `app/http/requests/<surface>/<feature>/<feature>_requests.go`,
  embed `requests.Open` (permission is route middleware), declare `Rules()` and,
  when the single error message matters, `FieldOrder()`.
- **Every exported field carries `form:"<name>"` beside `json:"<name>"`, spelled
  the same.** `form` is what the binder reads; without it a snake_case field
  binds nothing, silently. `TestEveryFormRequestFieldTagsFormAndJSONAlike`.
- **Every field bound into a number carries a type rule.** Amounts are strings
  (`decimal_string` rule) and are parsed with `pkg/amount`
  in the service, never `float64`.
- **A form request never touches the container or the ORM.** Context a rule
  needs (the wallet's chain for `blockchain_address`) comes from the scope
  middleware that already loaded the wallet: `middleware.CurrentWallet(ctx).Chain`.
  `db_exists:<table>,<column>` and `unique:<table>,<column>` (`app/rules`; Goravel
  v1.17 has no native ones) give the form a friendly 422 through the `RowCount`
  port. A rule written without its table and column fails, and a failed read
  passes. They are a courtesy: a write is still settled by the database
  constraint in the repository (SQLSTATE 23505 → 409).
- **A route parameter wins over a body or query field of the same name.**
- **Body-less POST → `ValidatePath`.**
- A check made after the form request passed answers with `responses.FieldError`.

## Rendering

- Success always goes through a resource in
  `app/http/resources/<surface>/<feature>/`, never a model, never an ad-hoc
  `map`, never a `WalletView{*models.Wallet}` embedding a model.
- Paginated lists keep the envelope `{data,total,limit,offset}` through
  `resources.Page` and `requests.Paging`.
- **A success body is written with the Goravel idiom**: `ctx.Response().Success().Json(body)`
  for 200, `ctx.Response().Status(http.StatusCreated).Json(body)` (or `Json(status, body)`)
  for any other 2xx, `ctx.Response().NoContent()` for 204. Permanent rule.
  The bytes are `application/json; charset=utf-8` with no trailing newline;
  `responses.JSON` (trailing newline, `application/json`) belongs to the error
  family only, and a route keeps the writer it has because moving it changes the
  wire (`tests/contract`).
- `responses.InternalError(ctx, fmt.Errorf("doing x: %w", err))` for every
  unexpected error: it logs the cause and answers without it.
- A chain/RPC/provider failure the caller can retry is `responses.ProviderError`
  (502), with the endpoint's own message — never the provider's text, which can
  carry the RPC URL and its key.
- Swagger annotations live on handlers and reference `requests`/`resources`
  types only. No `*Swagger` duplicate structs in controllers.

## Where things go

- **File and type carry the feature name**: `wallets_controller.go` holding
  `WalletsController`; `wallets_requests.go`; `wallets_resource.go`. Besides
  them a controller package may hold only `errors.go` (its `mapError`) or
  `shared.go`. `TestControllerAndRequestFilesCarryTheirFeatureName`.
- One package-level `mapError(ctx, err, action)` per controller package: a
  `switch` of `errors.Is` cases, then `responses.InternalError`.
- Controllers hold no helpers of their own (`validateRequest`, `authorize`,
  `jsonError`, `MapInternalError`): `requests` reads, `responses` writes,
  `middleware` says who is asking, `app/policies` decides.
- **Controllers never import `app/repositories`, never call `facades.Orm()`,
  `facades.Mail()`, `facades.Crypt()`, `facades.Auth()` or `facades.Gate()`.**
  Those belong to services, providers and middleware.
- Middleware aborts with a `responses` writer and `.Abort()`: a new refusal
  with `responses.Error(ctx, status, code, message).Abort()`; an existing one
  keeps the writer it has (`responses.Fail`), because the bytes are part of the
  contract (`http-error-contract.md`, "The writers").

## The global chain

Installed once in `bootstrap/app.go` through `WithMiddleware`, replacing the
driver defaults: `RequestTimeout`, `RequestID`, `SecurityHeaders`, `BodyLimit`,
`CORS`, with `RecoverPanic` as the recovery. `tests/architecture/middleware_chain_test.go`
reads the effective chain back off `facades.Route()` after Boot.

`RequestTimeout` only installs a deadline on the request context; it never
answers and never spawns a goroutine (a goroutine that outlives the request
writes into a pooled gin context). The hard 504 is `middleware.TimeoutHandler`,
wrapped around the router by `pkg/lifecycle` in `runLocal`, with the handler
writing into a private buffer. Lambda mode has only the cooperative deadline.

Per-route guards are mounted **per verb in the route file**, in a fixed order:
`Throttle → Auth (SessionAuth | APITokenAuth) → Scope (Account → Wallet) → Permission`.
See [`authorization.md`](./authorization.md).
