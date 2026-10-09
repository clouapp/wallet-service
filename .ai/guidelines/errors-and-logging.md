# Errors & Logging Guideline

> Status: errors HOLD (`%w`, package sentinels, no text matching in controllers).
> Logging: the redacting sink is installed at boot for `facades.Log()`
> (`providers.InstallLogRedaction` from `bootstrap.bootConfig`). TARGET: the plain
> `log/slog` calls (about 240) still use Go's default handler and bypass it, so
> until `slog` is routed through the same handler a `slog` call must never carry
> a URL, a token or a secret.

## Errors

```go
// 1. The repository names the query. A miss is a sentinel.
func (r *WalletRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	var row models.Wallet
	if err := r.Query(ctx).Where("id", id).FirstOrFail(&row); err != nil {
		return nil, db.LookupError(err, "find wallet") // a miss is models.ErrRepositoryNotFound
	}
	return &row, nil
}

// 2. The service owns the refusal the caller can act on.
if errors.Is(err, models.ErrRepositoryNotFound) {
	return nil, ErrWalletNotFound
}

// 3. The controller translates.
return mapError(ctx, err, "activating wallet")
```

### Rules

- **Errors live in the package that owns them**, in `errors.go` (or beside the code
  that returns them): `app/services/account/errors.go`,
  `app/services/chainregistry/errors.go`, the sweep and withdraw sentinels in their
  packages; vocabulary two layers share goes in `app/models/errors.go`
  (`ErrRepositoryNotFound`). **No central error package.**
- `ErrX = errors.New("pkg: lowercase message")`, one-line comment.
- Wrap with `%w`, and only to add what the caller cannot see. One layer owns
  each piece of context: the repository names the query, the controller names
  the request.
- A refusal the caller can act on is a sentinel matched with `errors.Is` — never
  `strings.Contains(err.Error(), ...)`.
- **Never `panic`** in a request, worker or scheduler path; never discard
  an error with `_`; no `os.Exit` outside `main`.
- **Fail closed** on authorization stores and rate/quota counters: unreachable
  is 503, never "allowed".
- An artisan command that fails returns the error and exits non-zero.
- Everything in a `.go` file is English.

## Logging — one redacted sink

One sink for every process: the Goravel log channel wrapped by a redacting handler
installed from `app/providers` (`InstallLogRedaction`, called from
`bootstrap.bootConfig` before the providers run). Every message and field passes
through `RedactText` / `RedactError`, which strip URL userinfo and query strings
— **RPC URLs embed API keys in the path or query**, so the redactor also masks the
configured RPC hosts' secrets — including the framework's SQL log. A second path
(plain `slog`) is the TARGET gap named in the status line.

- Identifiers in the message, not in `With(...)`.
- Debug / Info / Warning / Error with their usual meaning; never `fmt.Println`.

## Never logged, at any level

- Passphrases, MPC shares (customer share A, service share B), share IVs/salts,
  derived keys, chain codes, private keys, seeds.
- Secrets Manager values (ARNs may be logged; values never).
- RPC URLs with credentials, provider API keys, webhook signing secrets, HMAC
  keys, `X-Signature` values.
- JWTs, refresh tokens, API tokens, TOTP secrets and codes, password-reset tokens.
- Request bodies of auth routes and of withdrawal creation.
- Customer e-mail addresses in error wraps (identify by id).

```go
// BAD
return responses.InternalError(ctx, fmt.Errorf("decrypting share for %s with %q: %w", user.Email, passphrase, err))
// GOOD
return responses.InternalError(ctx, fmt.Errorf("decrypting customer share for wallet %s: %w", wallet.ID, err))
```

## Internal strings never reach a client

`responses.Error` / `InternalError` / `ProviderError` are the only ways a failure
leaves the HTTP process, and none forwards a cause. `APP_DEBUG` is `false`
outside local.
