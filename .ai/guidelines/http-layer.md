# HTTP Layer Guideline

> Status: HOLDS. `tests/architecture` enforces what is marked as guarded
> (`http_layer_test.go`, `responses_test.go`, `layers_test.go`); the rest is
> convention. Handlers are methods on a feature controller built with a `Deps`
> struct, read the caller through `requestctx`, and write failures through
> `responses`.

The HTTP layer binds a request, calls **one** service, maps that service's
errors and renders a resource. Everything it needs lives in a few packages under
`app/http/`. A controller package never redefines them.

| Package | Use it for |
|---|---|
| `requests` | running a form request (`requests.Validate`), a UUID path parameter (`RouteUUID`) |
| `responses` | every failure body: `Fail`, `FailWith`, `FailMessage`, `Error`, `InternalError`, `ProviderError`, `ValidationFailed`, `FieldsFailed`, `FieldError` (see `http-error-contract.md`) |
| `resources` | the wire shape. **JSON tags exist only here** — a model carries none, not even `json:"-"` (`TestModels_Carry_NoWireTags`) |
| `pagination` | the list window (`pagination.ParseParams`, `ParseStrict`); the list envelope is `resources.Page` |
| `middleware` | who is asking and what they may reach: auth, account/wallet scope, permission guards (`Can`, `WalletCan`, `May*`), `Throttle`, `PlatformAdmin`. `middleware/requestctx` reads what they stored |

## Surfaces

| Surface | Prefix | Caller | Controllers |
|---|---|---|---|
| dashboard | `/v1` | a `User` with a session (Goravel JWT guard), acting in an account | `app/http/controllers/dashboard/<feature>/` |
| external | `/api/v1` | an `AccessToken` bound to ONE account (JWT + optional `X-Signature` HMAC) | `app/http/controllers/external/<feature>/` |
| platform | `/v1/platform` | a `User` with a `platform_admins` row | `app/http/controllers/platform/<feature>/` |
| inbound provider webhooks | `/v1/webhooks/ingest/{provider}/{chainID}` | a chain-data provider, authenticated by its signature | `app/http/controllers/ingest/` |
| public | `/health`, `/swagger/*` | anyone | `app/http/controllers/health/`, `routes/docs.go` |

**A handler serves one surface, and never asks which caller it has.** A handler
that reads `requestctx.User(ctx)` to find out whether the caller is a session or a
token is two handlers glued together, and the `if` decides security (e.g. whether
TOTP is required). When both surfaces expose the same operation with identical
behaviour (gas status, consolidate, addresses), ONE handler type lives in
`app/http/controllers` (`SweepHandler`, `AddressesHandler`); each surface's
controller (`dashboard/sweep`, `external/sweep`, …) wraps it, keeping its own
routes, guards and constructor. The handler takes the surface name only to label
logs. A copy per surface drifts. The Swagger annotations of a shared handler sit
on the handler, so they describe one operation (see "Swagger").

## The shape of a handler

```go
type WalletsController struct {
	wallets *wallet.Service
}

func NewWalletsController(deps WalletsControllerDeps) *WalletsController {
	return &WalletsController{wallets: deps.Wallets}
}

func (c *WalletsController) CreateWallet(ctx contractshttp.Context) contractshttp.Response {
	var req requests.CreateWalletRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	result, err := c.wallets.CreateWallet(ctx.Context(), requestctx.MustAccount(ctx).ID, req.Chain, req.Label, req.Passphrase)
	if err != nil {
		return mapError(ctx, err)
	}
	return ctx.Response().Status(http.StatusCreated).Json(walletresource.CreateFrom(result))
}
```

Four steps, in that order, and nothing else. Dependencies come through the
**constructor**: a `Deps` struct of the services the controller uses (concrete
types or the narrow interface the controller declares). No `container.Make` in a
controller (`TestControllers_Take_TheirDependenciesByConstructor`), no repository,
no package-level `var authService = …`. The route table builds the controller with
`container.MustMake[*T]()`, so a missing binding fails at boot, not on the first
request. Middleware and resources follow the same rule: a middleware factory takes
what it reads (`middleware.Can(accounts, perm)`) and a resource only shapes data
its controller already loaded. `container.Make/MustMake` stays in `bootstrap/`,
`routes/`, `app/providers/` and `main.go`.

## Who is asking

See [`identity-and-scope.md`](./identity-and-scope.md). In short: the auth
middleware stores the authenticated user or token on the request context, the
scope middleware adds the account and (on wallet routes) the wallet, and
handlers read them only through `requestctx` (`MustUserID`, `MustUser`,
`MustAccount`, `AccountRole`, `Wallet`, `MustWallet`, `APIToken`). A handler
behind the guard does not write that 401/404 itself. A string-keyed
`ctx.Value("account_id")` is refused outside `app/http/middleware`
(`TestContext_Values_AreReadOnlyInMiddleware`).

**The account and the wallet come from the path or the authenticated token,
never from the body.** On the dashboard the account comes from the
`X-Account-Id` header or `{accountId}` and is checked against the user's
membership; on the external API it is the token's account and nothing else.
An account or wallet id read off a payload turns every route into a
cross-account read.

## Validating a request

```go
var req requests.CreateWithdrawalRequest
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
  `Bind`, `Input`, `All` or `Json` itself — guarded by
  `TestHandlers_Read_TheirInputThroughAFormRequest`. The one exception is
  `requests.Bind`, for the webhook update that must keep a 400 on an empty body.
- **A path or query parameter is read in the handler**: `requests.RouteUUID(ctx,
  "walletId")` for a UUID, `ctx.Request().Route("id")` / `Query("limit")` for the
  rest, the documented Goravel way. A form request exists only where there are
  rules to run: no request that copies a parameter into a struct.
- Form requests live flat in `app/http/requests/<name>_request.go`, one per
  operation (`create_wallet_request.go`). They implement `Authorize`, `Rules` and,
  when the messages matter, `Messages`/`Filters`; permission is route middleware,
  so `Authorize` allows (`requests.Open` embeds that).
- **Every exported field carries `form:"<name>"` beside `json:"<name>"`, spelled
  the same.** `form` is what the binder reads; without it a snake_case field
  binds nothing, silently (`TestEvery_Form_RequestFieldTagsFormAndJSONAlike`).
- **Every field bound into a number carries a type rule.** Amounts are strings
  (`decimal_string` rule) and are parsed with `pkg/amount` in the service, never
  `float64`.
- **A form request never touches the container or the ORM.** Context a rule
  needs (the wallet's chain for `blockchain_address`) comes from the scope
  middleware that already loaded the wallet: `requestctx.MustWallet(ctx).Chain`.
  `db_exists:<table>,<column>` and `unique:<table>,<column>` (`app/rules`; Goravel
  v1.17 has no native ones) give the form a friendly 422 through the `RowCount`
  port. A rule written without its table and column fails, and a failed read
  passes. They are a courtesy: a write is still settled by the database
  constraint in the repository (SQLSTATE 23505 → 409).
- **A route parameter wins over a body or query field of the same name.**
- A check made after the form request passed answers with `responses.FieldError`.

## Rendering

- Success goes through a resource in `app/http/resources/...`, never a model,
  never an ad-hoc `map`, never a view struct embedding a model.
- Paginated lists keep the envelope `{data,total,limit,offset}` through
  `resources.Page` and `pagination.ParseParams`.
- **A success body is written with the Goravel idiom** (permanent rule, D1/D14):
  `ctx.Response().Success().Json(body)` for 200,
  `ctx.Response().Status(http.StatusCreated).Json(body)` (or `Json(status, body)`)
  for any other 2xx, `ctx.Response().NoContent()` for 204. The bytes are
  `application/json; charset=utf-8` with no trailing newline. `responses` has no
  success writer. `responses.JSON` (trailing newline, `application/json`) belongs
  to the error family, and a route keeps the writer it has because moving it
  changes the wire (`tests/contract`).
- `responses.InternalError(ctx, fmt.Errorf("doing x: %w", err))` for an
  unexpected error: it logs the cause and answers without it.
- A chain/RPC/provider failure the caller can retry is `responses.ProviderError`
  (502), with the endpoint's own message — never the provider's text, which can
  carry the RPC URL and its key.

## Swagger

Annotations (`// @Summary`, `@Router`, …) live on the handler. `docs/` is generated
with the version the Makefile pins (`make swagger-install`, then
`make swagger-generate`, `swag@v1.8.12`; a newer `swag` writes a different
document). Regenerate in the same change that edits an annotation.

- A description states what the code does today. Every `/v1/platform` route is
  refused with 403 by `PlatformAdmin` before anything else, so a description says
  an unknown target is 404 *for a platform admin*, never "404 before the admin
  check".
- Failures use `responses.ErrorBody` (the envelope, `app/http/responses/swagger_types.go`,
  documentation only). A type in another package is qualified (`controllers.X`,
  `responses.X`): swag does not resolve a bare name outside its own package, and it
  drops the operation of the file it fails on, with only a log line. After a
  regenerate, check that the log has no `ParseComment error` and that the number of
  operations in `docs/swagger.json` equals the number of `@Router` lines.
- The body and response doc types are the `*Swagger` structs beside the controller
  or in `app/http/controllers/swagger_types.go`, and the `resources` types.
- A throttled route (`/v1/auth/{register,login,2fa/verify,refresh,recover,
  recover/confirm}`, all of `/api/v1`, `gas-check`) lists a 429 `responses.ErrorBody`.

## Where things go

- Files carry the feature name: `wallets_controller.go` holding
  `WalletsController`; `create_wallet_request.go`; `wallets_resource.go`.
- Errors a controller maps to a status are matched with `errors.Is` against the
  service's sentinels. The helpers shared by several controllers live in
  `app/http/controllers/errors.go` (`MapInternalError`, `MapSweepError`,
  `MapWithdrawalCreateError`, `MapSpendingLimitError`) and `address_errors.go`;
  a controller package keeps a `mapError` of its own for its service's sentinels.
- Controllers hold no helpers for reading input, authorizing or writing a body of
  their own: `requests` reads, `responses` writes, `middleware` says who is
  asking, `app/policies` decides.
- **Controllers never import `app/repositories`, a database library, or call
  `facades.Orm()` / `DB()` / `App()`** (`TestHTTP_Layer_DoesNotReachPersistence`).
  Services, providers and middleware own persistence, mail and gates. Facades
  come through `app/facades` (`TestFramework_Facades_ComeThroughAppFacades`).
  Documented exceptions in controllers: `appfacades.Auth(ctx).LoginUsingID` mints
  the access token of a new session (`session_issue.go`, `auth_controller.go`) and
  reads the guard for the invite acceptance; `Crypt` is opened in
  `wallet_view.go` (a chain RPC URL, to name the network) and sealed in the TOTP
  setup of `user_controller.go` — both should move behind a service.
- Middleware aborts with a `responses` writer and `.Abort()`: a new refusal with
  `responses.Error(ctx, status, code, message).Abort()`; an existing one keeps the
  writer it has (`responses.Fail`), because the bytes are part of the contract
  (`http-error-contract.md`, "The writers").

## The global chain

Installed once in `bootstrap/app.go` through `WithMiddleware`
(`middleware.GlobalChain`), replacing the driver defaults: `RequestTimeout`,
`RequestID`, `SecurityHeaders`, `BodyLimit`, `CORS` and the inbound-signature
check, with `RecoverPanic` as the recovery.

`RequestTimeout` only installs a deadline on the request context; it never
answers and never spawns a goroutine (a goroutine that outlives the request
writes into a pooled gin context). The hard 504 is `middleware.TimeoutHandler`,
wrapped around the router by `pkg/lifecycle` in `runLocal`, with the handler
writing into a private buffer. Lambda mode has only the cooperative deadline.

Rate limiters are named in `bootstrap` (`middleware.RegisterThrottles`) before the
routes that use them; a route mounts `middleware.Throttle(name)`
(`http-error-contract.md`, "Rate limits").

Per-route guards are mounted **per verb in the route file**, in a fixed order:
`Throttle → Auth (SessionAuth | APITokenAuth) → Scope (Account → Wallet) → Permission`.
See [`authorization.md`](./authorization.md).
