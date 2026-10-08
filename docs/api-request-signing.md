# API request signing

Status: **v1 is what runs today. v2 is PROPOSED and not implemented.** Nothing in
this document changes the wire until the owner and the integrators (Markets,
`clouapp/back`) have agreed the canonical string below, because once someone
integrates against it, it is frozen.

Code: `app/http/middleware/api_token_auth.go`. Tests that pin v1:
`app/http/middleware/api_token_auth_test.go` (`TestV1_*`).

## v1: what runs today

The external API (`/api/v1`) authenticates with the account API token as a bearer
JWT. A request may also carry `X-Signature`.

| | |
|---|---|
| Header | `X-Signature: <64 lowercase hex>` |
| Key | the whole bearer JWT, as sent |
| Message | the request body, and nothing else |
| MAC | HMAC-SHA256, hex encoded, compared case-sensitively |
| When it is checked | whenever the header is present, even if the token does not require it |
| When it is required | only for a token whose JWT carries the `sig` claim (`require_signature` at mint time); a missing header is then `401 invalid_signature` "missing request signature" |
| Failure | `401`, `{"error":{"code":"invalid_signature","message":"invalid request signature"}}` |

The body is read to compute the MAC and handed back whole to the handler.

Which tokens require it: the create-token request has a `require_signature` field
that defaults to false, and the dashboard does not send it, so tokens minted from
the dashboard today are bearer-only, with an optional signature.

### What v1 does not give

- **No freshness.** There is no timestamp or nonce, so a captured request can be
  replayed as often as the receiver accepts it.
- **No binding to the request.** The message is the body only. A `GET` signs the
  empty string, so its signature is the same for every path and query for the life
  of the token, and a signed body can be replayed against another route.
- **No separate secret.** The key is the bearer. Whoever can read the token can sign
  with it, so the signature adds nothing against a leaked bearer. (The 32-byte
  `secret` claim inside the JWT is readable by the holder of the bearer too, so using
  it as the key would add nothing either.)

Until v2 exists, the control against a leaked bearer is the token's `ip_cidr`
allowlist (enforced on `middleware.ClientIP`, which trusts `X-Forwarded-For` only
from `TRUSTED_PROXIES`) and the rate limit on `/api/v1`.

## v2: proposed

### Wire format

```
X-Signature: t=<unix seconds>,n=<32 hex nonce>,v2=<64 hex>
```

- The same header name, so the CORS allow-list does not change.
- A v2 value cannot be a valid v1 value (v1 is bare lowercase hex), so the server
  dispatches on the format: a value containing `v2=` takes the v2 path, anything
  else takes the v1 path untouched. Today a v2-format value is `401` for every
  token (pinned by `TestV1_AV2FormatHeader_IsRefused_Today`), so accepting it later
  cannot break a client that works now.
- Do **not** dispatch on `X-Timestamp`: it is in the CORS allow-list because of the
  retired `X-API-Key` scheme, and an old integrator may still send it.

### Canonical string

```
"v2\n" + t + "\n" + n + "\n" + METHOD + "\n" + REQUEST_URI + "\n" + hex(sha256(body))
```

- `t`, `n`: the values from the header, as sent.
- `METHOD`: upper case.
- `REQUEST_URI`: path, then `?` and the raw query when there is one, byte for byte as
  sent (no reordering or re-encoding).
- `hex(sha256(body))`: lowercase; the digest of the empty string for no body.

`v2` is the HMAC-SHA256 of that string, hex encoded, keyed with the token's
signing secret.

### Key

A per-token `signing_secret`: 32 random bytes, hex encoded, stored **sealed** with
the project's Crypt helper in a new nullable `access_tokens.signing_secret` column
(it cannot be hashed: the server has to recompute the HMAC). It is not the bearer
and not the existing `secret` claim.

### Freshness and replay

- Window: `|now - t| <= 300` seconds, else `401 invalid_signature` "expired request
  signature".
- Replay: `SET NX apisig:<token_id>:<nonce>` in Redis with a 600 s TTL; a nonce seen
  before is `401 invalid_signature` "replayed request signature". If Redis cannot be
  reached the request is `503 unavailable` (fail closed, as `http-error-contract.md`
  says for a store we fail closed on).
- The two new messages map to the existing `invalid_signature` code
  (`responses.messageCodes`), so clients get no new code.

### Issuing the secret

- `POST /v1/accounts/{id}/tokens` gains an additive response field `signing_secret`,
  shown once. `tests/contract` adds it to its volatile fields.
- Existing tokens get `POST /v1/accounts/{id}/tokens/{tokenId}/signing-secret`
  (rotate), behind `Can(tokens.write)`.
- The front end must show the secret once; without that change every secret minted
  is thrown away unseen.

### Opting in

Per request, by sending the v2 format. Per-token enforcement is a new column
`signature_scheme` (`NULL` keeps today's rules, `v2` refuses a v1 value), so an
integrator can move over and then close the v1 door for its own tokens.

## Rollout

1. Agree the canonical string with Markets and `clouapp/back`; this document is the
   contract, and it is versioned (`v2` is in the string, so a change is a `v3`).
2. Migration + sealed column + the issue and rotate endpoints + the front change.
3. Server accepts v2 next to v1. Integrators migrate; `signature_scheme = v2` per token.
4. v1 stays until no token uses it.

## Open questions for the owner and Markets

- Is a 300 s window right for their clocks, and do they need a longer replay TTL?
- Do they sign through a proxy that rewrites the query string? (`REQUEST_URI` is
  taken as received by the API, after the proxy.)
- Is `signature_scheme` per token enough, or should the platform be able to require v2
  for an account (`api-request-signature-required` feature flag)?
