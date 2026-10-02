# Identity and scope

> Status: PARTLY HOLDS. Authentication and membership already live in
> middleware. TARGET: typed accessors and unexported context keys instead of
> `ctx.Value("string")`. Migration: alignment prompt (Part 1) §3.3, §3.9.

## Who is asking

Exactly one of two actors, decided by the ROUTE's surface, never by a body
field or header:

| Actor | Surface | Authenticated by | Put on the context by |
|---|---|---|---|
| `*models.User` | dashboard `/v1` | Goravel JWT guard (`facades.Auth(ctx).Parse`), refresh token rotation, optional TOTP | `middleware.SessionAuth` |
| `*models.AccessToken` (API token) | external `/api/v1` | JWT (`sub: api_token`, `jti` = token row, `account_id` claim) signed with `jwt.secret`; the token row must exist and be active; when the token carries `require_signature`, an `X-Signature` HMAC over the body is mandatory | `middleware.APITokenAuth` |

An API token belongs to ONE account and acts only inside it. A user acts in the
accounts they are a member of.

The actor lives on the request context under an **unexported** key in
`app/http/middleware`; only the auth middleware can set it, and
`middleware.CurrentUser(ctx)` / `CurrentAPIToken(ctx)` are the only readers.
Do not add an exported setter: a `WithUser(ctx, u)` that any package can call
is a context every policy then trusts.

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
(`CurrentAccount`, `CurrentWallet`, `CurrentAccountRole`). Handlers and services
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

No string literal keys. The middleware package declares unexported key types;
`ctx.Value("wallet").(*models.Wallet)` (unchecked assertion on a string key) is
exactly what this replaces. `tests/architecture` refuses `ctx.Value("` outside
`app/http/middleware`.
