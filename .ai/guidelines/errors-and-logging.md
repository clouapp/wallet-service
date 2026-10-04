# Errors & Logging Guideline

> Status: errors mostly HOLD (`%w`, package sentinels). Logging is TARGET: today
> `log/slog` and `facades.Log()` coexist and there is no redacting sink.
> Migration: alignment prompt (Part 1) §3.11.

## Errors

```go
// 1. The repository names the query. A miss is a sentinel.
func (r *WalletRepository) FindByID(ctx context.Context, id uuid.UUID, lock bool) (*models.Wallet, error) {
	var row models.Wallet
	if err := r.Query(ctx)...First(&row); err != nil {
		return nil, db.NotFound(err, "find wallet")
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

- **Errors live in the package that owns them**, in `errors.go`:
  `app/services/errors.go`, `app/services/sweep/errors.go`,
  `app/services/withdraw/errors.go`, `app/adapters/chain/errors.go`,
  `app/http/middleware/errors.go`; vocabulary two layers share goes in
  `app/models/errors.go`. **No central error package.**
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

One logger for every process: `facades.Log()` wrapped by a redacting handler
installed from `app/providers` (port of slotkit's `log_redaction.go`). Every
message and field passes through `RedactText` / `RedactError`, which strip URL
userinfo and query strings — **RPC URLs embed API keys in the path or query**,
so the redactor also masks the configured RPC hosts' secrets — including the
framework's SQL log. If `slog` stays anywhere, it is the same redacting handler
behind `slog`, not a second path.

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
