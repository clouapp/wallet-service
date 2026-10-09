# Identity and scope

> Status: HOLDS. Authentication, membership and wallet scope live in middleware;
> handlers read the actor and the scope through the typed accessors of
> `app/http/middleware/requestctx`. A `ctx.Value("…")` string-keyed read or write
> outside `app/http/middleware` is refused
> (`TestContext_Values_AreReadOnlyInMiddleware`).

## Who is asking

Exactly one of two actors, decided by the ROUTE's surface, never by a body
field or header:

| Actor | Surface | Authenticated by | Put on the context by |
|---|---|---|---|
| `*models.User` | dashboard `/v1`, platform `/v1/platform` | Goravel JWT guard (`appfacades.Auth(ctx).Parse`), refresh token rotation, optional TOTP; the user must be active, not suspended, and the token not revoked | `middleware.SessionAuth` |
| `*models.AccessToken` (API token) | external `/api/v1` | JWT (`sub: api_token`, `jti` = token row, `account_id` claim) signed with `jwt.secret`; the token row must exist and be active; when the token carries `require_signature`, an `X-Signature` HMAC over the body is mandatory | `middleware.APITokenAuth` |

An API token belongs to ONE account and acts only inside it. A user acts in the
accounts they are a member of.

Only the auth middleware writes the actor onto the request context (the keys are
the constants of `requestctx`, `KeyUser`, `KeyUserID`, `KeyAPIToken`, …), and
`requestctx.User(ctx)` / `MustUser` / `UserID` / `APIToken` are the readers
everyone else uses. The keys are plain strings, so the architecture test, not the
type system, keeps other packages from writing them. Do not add an exported
setter: a `WithUser(ctx, u)` that any package can call is a context every
policy then trusts.

## Ending a dashboard session

A dashboard JWT carries only `key`, `sub`, `iat` and `exp`, with `iat` in whole
seconds, so two tokens of one user minted in the same second are byte-identical.
That rules out per-token revocation (the guard's `Logout` blacklists the token
string, which a login in the same second would mint again and find refused).

Every way of ending a session (logout, password change or reset, TOTP disable,
platform revoke) goes through `auth.SessionRevoker.RevokeAll`. **Logout therefore
ends every session of the user**, on all devices: it also deletes the refresh
tokens and writes `user.sessions_revoked` to the activity log. The access token
of another device is refused at once with 401 `session revoked` (it used to live
until it expired, at most 15 minutes). The rule:

- The watermark (`users.sessions_revoked_at`) is the start of the NEXT second
  after the revocation, so the whole revocation second is void.
- `SessionAuth` refuses a token whose `iat` is before the watermark.
- A new session is minted only after the watermark (`AwaitIssuable` waits out
  the rest of the second, at most ~1s), so its `iat` is at or after it and it
  differs from every token issued before.

Do not blacklist individual tokens and do not add a sub-second discriminator:
the guard offers no way to set a `jti`.

## The scope chain

```
actor → account → wallet
```

| Step | Dashboard | External |
|---|---|---|
| account | `X-Account-Id` header (`AccountHeader`) or `{accountId}` (`AccountContext`), checked against an ACTIVE `account_users` row of the user | the token's `account_id`, nothing else |
| wallet | `{walletId}` (`WalletContext`): the wallet must belong to the account, or the user must hold an active `wallet_users` row | `{walletId}` (`APIWalletContext`): `wallet.account_id` must equal the token's account, else **404** |
| chain capability | `UTXOOnly` on UTXO-only routes | same |

Each step loads the row once and puts it on the context
(read with `requestctx.Account`, `Wallet`, `AccountRole`). Handlers and services
receive the loaded rows; nobody reloads them by id.

Rules:

- **The id comes from the path or the token, never from the body.**
- **404 before 403.** A wallet that is not the caller's answers 404 on the
  external API (the id is not confirmed), and an unknown account is 404 before a
  missing membership is 403.
- A suspended/archived/frozen account or wallet is refused by the scope step,
  not by each handler. Reads may be allowed; mutations are not. The rule is
  written once, in the middleware and in the policy.
- **The account environment is part of the scope.** An account is `test` or
  `prod` (`accounts.environment`); a `test` account may only touch testnet
  chains and a `prod` account only mainnet chains. That check lives in the
  wallet/chain scope step and in `app/policies`, never in a handler.

## Context keys

The keys are the string constants of `requestctx`. Nobody reads or writes
`ctx.Value("wallet").(*models.Wallet)` (an unchecked assertion on a string
literal): the readers do a checked assertion and return `(value, ok)`, and the
`Must*` variants panic only where the route guarantees the value.
`tests/architecture` refuses `ctx.Value("…")` / `WithValue("…")` outside
`app/http/middleware`.
