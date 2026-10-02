# Authorization Guideline

> Status: TARGET. Today 15 Gate abilities are checked INSIDE handlers through
> `authorize(...)` (18 sites), the policies re-query membership and compare role
> strings, and six mutating handlers have no permission check at all (see the
> inventory). Migration: alignment prompt (Part 1) §3.9 — reviewed as a security change.

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

## Second factor and signatures

- Moving funds from the dashboard (create withdrawal, approve, change
  whitelist, change webhook endpoints) requires TOTP when the user has it on;
  re-authentication is a route-level or service-level rule, never an `if` on
  "is this a session caller" inside a shared handler.
- External tokens minted with `require_signature` must carry a valid
  `X-Signature` HMAC; `APITokenAuth` refuses before anything reads the body.

## Policy conventions

- Ability names and argument keys are constants in the policy's own file.
- Policies take primitives, `models` and `dtos` — never a `services` type
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

Until decided, these are reachable by ANY member of the account (or any token of
the account). Closing them may remove access someone has today.

## Adding a route

A new route is authenticated and scoped unless it is in the explicit exceptions
list in `CLAUDE.md` (health, docs, inbound provider webhooks).

**Its chain is written down before it merges**:
`tests/architecture/route_security_test.go` is the closed table of every (verb,
path) and the ordered guards it runs behind, checked against the booted router
in both directions — a route with no row fails, a row that does not match fails.
