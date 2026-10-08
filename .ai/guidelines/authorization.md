# Authorization Guideline

> Status: TARGET for the three layers below. This branch already runs the
> middleware order and the rank rule in `app/policies`, `APIScope` on the
> external token catalog, and `tokens.read` / `tokens.write` / `settings.view` /
> `settings.update` / `activity.read`. Gate abilities are still asked from
> handlers. Migration of those calls: alignment prompt (Part 1) §3.9.

Every authorization decision belongs in exactly one of three places, and **what
the decision depends on picks the place.**

| The decision depends on | Put it in | Answered by |
|---|---|---|
| the route alone (role in the account/wallet) | route middleware | `middleware.HasPermission(policies.WalletWithdraw)` |
| the **resource** being touched | a Gate ability in `bootstrap/gates.go` | a policy in `app/policies` |
| the request **body** or an argument (amount, destination, approvals) | the service, asking a policy | `policies.AccessPolicy` / `WalletPolicy` |

A permission checked inside a handler runs **after** the body was validated,
so a caller who may not act learns whether their payload was well formed. That
is why no handler calls `facades.Gate()` or an `authorize` helper.

`app/policies` is the only code that answers "who may do what". The middleware
asks it, the services ask it, and the Gate abilities delegate to it.

## Middleware order

Scope is route middleware, outer first: the session (or the API token), then
the account or the wallet, then the permission. A later step does not run when
an earlier one refuses. `Cors` and `CacheControl` are not guards.

| Surface | Order |
|---|---|
| dashboard account `/v1/accounts/{accountId}` | `SessionAuth` → `AccountContext` → `TOTPEnrollment` |
| dashboard wallet `/v1/wallets` | `SessionAuth` → `AccountHeader` → `TOTPEnrollment`, then `WalletContext` on the nested `/{walletId}` group, then `UTXOOnly` on unspents |
| external `/api/v1` | `APITokenAuth` (includes `ip_cidr`) → `APIWalletContext` on `/{walletId}` (404 before the scope check) → `APIScope(permission)` on the routes the token catalog names |
| platform `/v1/platform` | `SessionAuth` → `PlatformAdmin` on the whole group |
| guest `/v1/auth/*` except logout | no auth middleware |
| public `/health`, `/swagger/*` | no auth middleware |
| inbound `/v1/webhooks/ingest/...` | `ProviderSignature` checks the provider signature before the body is parsed |

`AccountContext` answers 404 for an unknown account and 403 when the caller has
no active membership. `APIScope` answers 403 when a token that lists
permissions does not hold the route's permission. A blank permissions store
keeps the previous access. A blank `ip_cidr` does the same for the allowlist.

`Can(perm)` is route middleware after `AccountContext` (and `TOTPEnrollment`,
which already sits on that group). It asks `policies.Can` with the role the
account middleware stored. The routes that use it are
`GET /v1/accounts/{accountId}/users` and
`GET /v1/accounts/{accountId}/invites`
(`users.read`: owner, admin, auditor), and
`POST /v1/accounts/{accountId}/invites`
(`users.write`: owner and admin). Auditor and user do not hold `users.write`.
The invite role is an argument, so `MayGrant` stays in `app/policies` and the
service asks it after the body is valid. A role above the caller is 403.
A missing permission is 403 `forbidden`. There is no `WalletCan(perm)` on this
branch: the wallet routes the plan names already refuse through
`RequireFundAction`, and the code catalog would let `user` withdraw and sweep.
Where another permission is already decided, the service asks `app/policies`
(`settings.view`, `settings.update`, `activity.read`) or the controller asks it
(`tokens.read`, `tokens.write`, the wallet Gate abilities). A new route still
writes its chain down before it merges.

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
  database (today's policies re-read `account_users` through the container).
- `auditor` is read-only by definition: every mutating ability denies it.

## Failing closed

- A nil or empty role set denies.
- An empty permission/ability name denies (it is a wiring bug).
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

- Ability names and argument keys are constants in the policy's own file.
- Policies take primitives and `models` — never a `services` type
  (`TestPoliciesLayerIsBelowServices`).
- Ask with the request context:
  `facades.Gate().WithContext(ctx).Allows(policies.WalletCancelWithdrawal, map[string]any{...})`.

## Inventory (fill in during the migration — the TARGET column is the work list)

### Abilities that exist today (`AuthServiceProvider.Boot`)

`account.view`, `account.update`, `account.delete`, `account.add-user`,
`account.remove-user`, `account.freeze`, `account.archive`,
`account.manage-tokens`, `wallet.view`, `wallet.update`, `wallet.freeze`,
`wallet.add-user`, `wallet.remove-user`, `wallet.whitelist`,
`wallet.manage-webhooks`, `wallet.cancel-withdrawal`.

Each must end as a route permission (layer 1) or a Gate ability asked by its
service (layer 2) — never a call inside a handler.

### Mutating handlers with NO permission check today

| Handler | Surface(s) | Target permission | Decided by |
|---|---|---|---|
| `CreateWalletWithdrawal` | dashboard + external | `wallet.withdraw` | **product decision** |
| `ConsolidateWallet` | dashboard + external | `wallet.sweep` | **product decision** |
| `GenerateAddress` | dashboard + external | `wallet.addresses.write` | **product decision** |
| `UpdateAddress` | dashboard + external | `wallet.addresses.write` | **product decision** |
| `ActivateWallet` | dashboard | `wallet.update` | **product decision** |
| `UpdateWalletSettings` | dashboard | `wallet.update` | **product decision** |

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
