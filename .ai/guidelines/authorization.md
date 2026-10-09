# Authorization Guideline

> Status: HOLDS for the three layers below, guarded by
> `TestPermission_Decisions_GoThroughThePolicy` and the route-security table. The
> middleware order and the rank rule live in `app/policies`, `APIScope` enforces
> the external token catalog, and `tokens.read` / `tokens.write` / `settings.view`
> / `settings.update` / `activity.read` are route guards. Every user-permission
> route guard asks the Gate through `middleware.authorize`. One handler check is
> left: `UpdateWalletSettings` asks `policies.WalletUpdate` itself (through
> `controllers.Deny`; wallet or account owner/admin), after WalletContext and
> before it reads the body. The open items are the product decisions in the
> inventory at the end.

Every authorization decision belongs in exactly one of three places, and **what
the decision depends on picks the place.**

| The decision depends on | Put it in | Answered by |
|---|---|---|
| the route alone (role in the account/wallet) | route middleware | `middleware.Can(accounts, middleware.PermUsersRead)`, asking the Gate |
| the **resource** being touched | route middleware that resolves the resource, then asks a Gate ability | a policy in `app/policies`, registered by `policies.DefineGates` |
| the request **body** or an argument (amount, destination, approvals) | the service, asking a policy function | `policies.MayGrant`, `policies.MayPerformFundAction` |

A permission checked inside a handler runs **after** the body was validated,
so a caller who may not act learns whether their payload was well formed. That
is why no handler calls `facades.Gate()` or an `authorize` helper.

`app/policies` is the only code that answers "who may do what". The Gate
abilities delegate to it, the route guards ask the Gate, and the services ask
the policy functions directly.

## The Gate

The abilities are defined in `app/policies/gates.go` (`DefineGates`) and
registered from `bootstrap/app.go` through `WithCallback`, as the Goravel
skeleton does; there is no `bootstrap/gates.go` and no `AuthServiceProvider`.
An ability decides from its arguments alone: the guard loads the role, the
grants or the membership and passes them under the `policies.Arg*` keys. A
missing or mistyped argument refuses.

| Abilities | Decide with | Asked by |
|---|---|---|
| one per account permission (`users.read`, `tokens.write`, `addresses.create`, …) | `policies.Can` over `ArgGrants` | `Can(accounts, perm)`, `WalletCan(perm)` |
| `account.update-member` | `users.write` over `ArgGrants`, refused with the member sentence | `AccountUpdateMember` |
| `account.view-settings`, `account.update-settings`, `account.read-activity`, `account.view-features` | `MayViewSettings`, `MayUpdateSettings`, `MayReadActivity` on `ArgAccountRole` | the four `May*` guards |
| `wallet.freeze`, `wallet.archive`, `wallet.add-user`, `wallet.remove-user`, `wallet.whitelist`, `wallet.manage-webhooks`, `wallet.cancel-withdrawal` | the `Wallet*` policy of the same name on the loaded membership (and `ArgCreatorID`) | the `Wallet*` guard of the same name |

Every guard is `middleware.authorize(gate, ability, subject)`. The subject
resolves the child the path names first (so 404 stays ahead of 403, and a
failed read is 503), then hands the Gate its arguments; a refusal is 403 with
the ability's sentence, written by `responses.FailMessage`. The Gate answers an
undefined name with "ability doesn't exist: …", which must never reach a body,
so `authorize` refuses to build a guard for a name `policies.IsAbility`
rejects, and booting the routes fails. The refusal sentences are constants in
`app/policies` (`MsgSettingsViewDenied`, …); the services build their
sentinels from them.

Only `app/policies`, `app/providers` and `app/http/middleware` may call
`facades.Gate()` (`TestPermission_Decisions_GoThroughThePolicy`). Services
never ask the Gate: they call the policy function the ability wraps.

## Middleware order

Scope is route middleware, outer first: the session (or the API token), then
the account or the wallet, then the permission. A later step does not run when
an earlier one refuses. `Cors` and `CacheControl` are not guards.

| Surface | Order |
|---|---|
| dashboard account `/v1/accounts/{accountId}` | `SessionAuth` → `AccountContext` → `TOTPEnrollment` |
| dashboard wallet `/v1/wallets` | `SessionAuth` → `AccountHeader` → `TOTPEnrollment`, then `WalletContext` on the nested `/{walletId}` group, then `UTXOOnly` on unspents |
| external `/api/v1` | `APITokenAuth` (includes `ip_cidr`) → `APIWalletContext` on `/{walletId}` (404 before the scope check) → `APIScope(lookups, permission)` on the routes the token catalog names |
| platform `/v1/platform` | `SessionAuth` → `PlatformAdmin` on the whole group |
| guest `/v1/auth/*` except logout | no auth middleware |
| public `/health`, `/swagger/*` | no auth middleware |
| inbound `/v1/webhooks/ingest/...` | `ProviderSignature` checks the provider signature before the body is parsed |

`AccountContext` answers 404 for an unknown account and 403 when the caller has
no active membership. `APIScope` answers 403 when a token that lists
permissions does not hold the route's permission. A blank permissions store
keeps the previous access. A blank `ip_cidr` does the same for the allowlist.

`Can(accounts, perm)` is route middleware after `AccountContext` (and `TOTPEnrollment`,
which already sits on that group). It asks the Gate for the permission's
ability, which decides with `policies.Can` on the grants the account
middleware stored. The routes that use it are
`GET /v1/accounts/{accountId}/users` and
`GET /v1/accounts/{accountId}/invites`
(`users.read`: owner, admin, auditor), and
`POST /v1/accounts/{accountId}/invites`
(`users.write`: owner and admin). Auditor and user do not hold `users.write`.
The invite role is an argument, so `MayGrant` stays in `app/policies` and the
service asks it after the body is valid. A role above the caller is 403.
A missing permission is 403 `forbidden`. `WalletCan(perm)` exists but no route
registers it: the wallet routes the plan names already refuse through
`RequireFundAction`, and the code catalog would let `user` withdraw and sweep.
`tokens.read` and `tokens.write` are `Can` on the token routes; the wallet
abilities are the `Wallet*` guards; `settings.view`, `settings.update` and
`activity.read` are the `May*` guards, and their services ask `app/policies`
again for callers that are not HTTP. A new route still writes its chain down
before it merges.

`GET /v1/wallets/{walletId}` is registered beside `WalletContext`, not inside
it. The nested group (`/activate`, addresses, users, and the rest) is the one
that runs `WalletContext`.

## Rank rule

Account roles are one ladder, decided only in `app/policies`
(`member_rank.go`):

| Role | Rank |
|---|---|
| `owner` | 3 |
| `admin` | 2 |
| `auditor`, `user` | 1 |

An unknown role has no rank and fails closed. `MayGrant(actor, granted)` and
`MayActOn(actor, target)` allow an equal or lower rank and refuse a higher
one. Auditor and user share a rank; `ManagesMembers` is still owner and admin
only.

`models.AccountRoleOutranks` is that comparison. Only `app/policies` may call
it (`TestOnlyPoliciesCallAccountRoleOutranks`). The function is not declared
on this branch; the test fails when a call shows up in a service, a handler,
or anywhere else.

Platform RBAC pivots (`model_has_roles`, `role_has_permissions`,
`model_has_permissions`) are not on this branch. Only `app/policies` may read
them (`TestOnlyPoliciesReadRBACPivots`). A migration may create the tables. A
repository, a service, or a handler may not query them.

## Roles

| Where | Roles | Notes |
|---|---|---|
| `account_users.role` | `owner`, `admin`, `auditor`, `user` | one role per membership |
| `wallet_users.roles` | a set (stored as text today) | wallet-level grants on top of the account role |

- Roles are **constants** in `app/models` (`models.AccountRoleOwner`…), never
  string literals in a policy.
- Ranking and comparison live **only** in `app/policies`
  (`TestOnlyPoliciesRankRoles`). A second `role == "owner" || role == "admin"`
  elsewhere is how a rule ends up true on one path and false on another.
- A policy is **stateless**: it decides from the account role and wallet roles
  the scope middleware already loaded and passes in. It does not query the
  database; the route guard loads the membership and passes it as arguments.
- `auditor` is read-only by definition: every mutating ability denies it.

## Failing closed

- A nil or empty role set denies.
- A permission or ability name the Gate does not define is a wiring bug:
  `authorize` refuses to build the guard, so the routes do not boot.
- A missing or wrongly-typed Gate argument denies — comma-ok assertions only;
  a policy never panics.
- A failed read of a membership denies (403/503), never admits.

## Keep 404 before 403

Resolve the resource, answer 404 if it does not exist (or is not the caller's
on the external API), then authorize.

The exception is everything under `/v1/platform`: `PlatformAdmin` checks
`platform_admins` for the whole group before any handler resolves its target, so
a caller who is not an admin gets 403 whether or not the account, user, chain or
settings group exists. The 403 sentence is the one the route's service returns
(`routes/platform_refusals.go`); the services keep their own check as well, for
callers that are not HTTP. `tests/feature/api/platform/guard` fails when a
platform route answers a member with anything but that 403.

## Second factor and signatures

- Moving funds from the dashboard (create withdrawal, approve, change
  whitelist, change webhook endpoints) requires TOTP when the user has it on;
  re-authentication is a route-level or service-level rule, never an `if` on
  "is this a session caller" inside a shared handler.
- External tokens minted with `require_signature` must carry an `X-Signature`
  header. A signature that is present is verified whether or not the token
  requires one: `APITokenAuth` reads the body to compute the HMAC (key: the whole
  bearer JWT, message: the body only) and hands it back to the handler. A token
  without the claim, which is every token the dashboard mints today, is bearer-only
  and an optional signature. The scheme's limits and the proposed v2 are in
  [`docs/api-request-signing.md`](../../docs/api-request-signing.md).

## Policy conventions

- Ability names, argument keys and refusal sentences are constants in
  `app/policies`.
- Policies take primitives and `models` — never a `services` type
  (`TestPoliciesLayerIsBelowServices`).
- The Gate is asked only by `middleware.authorize`, with the request context:
  `gate.WithContext(ctx).Inspect(ability, arguments)`. A service asks the
  policy function (`policies.MayUpdateSettings(role)`), never the Gate.

## Inventory (open product decisions)

### Abilities

`policies.Abilities()` lists them; the table in "The Gate" groups them. A new
ability goes into `gateAbilities` in `app/policies/gates.go`, with its pair in
`gates_test.go`.

### Mutating handlers with NO permission check today

| Handler | Surface(s) | Permission to add (TARGET) | Decided by |
|---|---|---|---|
| `Store` (withdrawals) | dashboard + external | `wallet.withdraw` | **product decision** |
| `ConsolidateWallet` | dashboard + external | `wallet.sweep` | **product decision** |
| `GenerateAddress` | dashboard + external | `wallet.addresses.write` | **product decision** |
| `UpdateAddress` | dashboard + external | `wallet.addresses.write` | **product decision** |
| `ActivateWallet` | dashboard | `wallet.update` | **product decision** |

On the external API, create wallet, generate address, consolidate, and create
withdrawal now run `APIScope` with `wallets.create`, `addresses.create`,
`sweep.execute`, and `withdrawals.create`. A token with a blank permissions
store still reaches them. Update address has no `APIScope`. The dashboard
handlers in this table still check membership only. Closing that gap may
remove access someone has today.

## Adding a route

A new route is authenticated and scoped unless it is in the explicit exceptions
list in `CLAUDE.md` (health, docs, inbound provider webhooks).

**Its chain is written down before it merges**:
`tests/architecture/routesecurity/route_security_test.go` is the closed table of
every (verb, path) and the ordered guards it runs behind.
`TestEveryRouteIsInTheRouteTable` checks the table against the booted router in
both directions. `TestGuardChainMatchesRegistration` checks each row's chain
against the middleware the route files register, and fails on a route with no
row, a row the router does not serve, or a chain that does not match.
